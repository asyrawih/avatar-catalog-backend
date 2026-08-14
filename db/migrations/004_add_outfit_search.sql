-- Migrasi: pencarian nama outfit (pg_trgm + pgvector).
--
-- Idempoten dan aman dijalankan pada database mana pun (baru maupun lama) —
-- pada database baru db/init/001_schema.sql sudah berbentuk akhir, jadi setiap
-- pernyataan di sini jadi no-op. Diterapkan otomatis oleh Job
-- avatar-catalog-db-migrate (lihat k8s/base/migrate-job.yaml) sebelum setiap
-- rollout api; tidak perlu dijalankan manual.
--
-- PRASYARAT: image Postgres harus pgvector/pgvector:pg17 (lihat
-- docker-compose.yml) — image postgres:17-alpine tidak membawa extension
-- vector.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS vector;

-- Operator <% (word_similarity) dipakai query pencarian; ambang bawaannya 0.6
-- terlalu ketat untuk salah ketik ("zepeto" vs "ZAPPETO" cuma ~0.4), jadi
-- diturunkan ke 0.3 di level database supaya berlaku untuk semua koneksi.
DO $$ BEGIN
    EXECUTE format('ALTER DATABASE %I SET pg_trgm.word_similarity_threshold = 0.3', current_database());
END $$;

-- NULL berarti belum (atau tidak akan) di-embed — pencarian tetap jalan lewat
-- jalur trigram. Dimensi 1536 mengikuti model text-embedding-3-small.
ALTER TABLE outfit ADD COLUMN IF NOT EXISTS name_embedding vector(1536);

-- Pencarian nama toleran salah ketik lewat operator trigram (<%).
CREATE INDEX IF NOT EXISTS outfit_name_trgm_idx
    ON outfit USING gin (name gin_trgm_ops);

-- Pencarian tetangga terdekat cosine di embedding nama. Parsial: baris yang
-- belum di-embed tidak membebani index.
CREATE INDEX IF NOT EXISTS outfit_name_hnsw_idx
    ON outfit USING hnsw (name_embedding vector_cosine_ops)
    WHERE deleted_at IS NULL AND name_embedding IS NOT NULL;

COMMIT;
