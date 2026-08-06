-- Skema outfit avatar.
--
-- Katalog sudah dilepas: backend tidak lagi menyimpan CATALOG_ITEM, CREATOR,
-- SECTION, BUNDLE, maupun SYNC_RUN. Roblox yang memegang kebenaran soal asset,
-- dan detail item yang perlu dirender ikut disimpan di OUTFIT_ITEM sebagaimana
-- dilaporkan klien.
--
-- Skrip ini dijalankan Postgres hanya saat volume data masih kosong. Untuk
-- memuat ulang: docker compose down -v && docker compose up.

BEGIN;

-- --- Extension pencarian ----------------------------------------------------

-- pg_trgm melayani pencarian nama yang toleran salah ketik; vector (pgvector)
-- menyimpan embedding nama untuk pencarian makna lintas bahasa.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS vector;

-- Operator <% (word_similarity) dipakai query pencarian; ambang bawaannya 0.6
-- terlalu ketat untuk salah ketik ("zepeto" vs "ZAPPETO" cuma ~0.4), jadi
-- diturunkan ke 0.3 di level database supaya berlaku untuk semua koneksi.
DO $$ BEGIN
    EXECUTE format('ALTER DATABASE %I SET pg_trgm.word_similarity_threshold = 0.3', current_database());
END $$;

-- --- Pemain dan rig dasar ---------------------------------------------------

-- Backend hanya tahu userId saat outfit atau transaksi pertama masuk; username
-- menyusul dari game server. Karena itu kolom teks punya default kosong agar
-- baris minimal cukup untuk memenuhi foreign key tanpa mengarang data.
CREATE TABLE player (
    user_id     bigint PRIMARY KEY,
    universe_id bigint,
    username    text        NOT NULL DEFAULT '',
    last_seen   timestamptz NOT NULL DEFAULT now()
);

-- Registry rig avatar. Rig di-upload ke Roblox lebih dulu, dan template_id
-- ADALAH Roblox asset id hasil upload itu (disimpan sebagai teks digit, karena
-- itu bentuk yang dikirim klien). Roblox yang memegang asetnya; tabel ini hanya
-- mencatat rig mana saja yang dipakai supaya FK OUTFIT.template_id tetap berarti.
--
-- name dan gender boleh kosong: saat rig pertama kali dipakai backend belum
-- tahu keduanya, dan diisi belakangan lewat PATCH /v1/templates/{templateId}.
CREATE TABLE body_template (
    template_id     text PRIMARY KEY CHECK (template_id ~ '^[1-9][0-9]*$'),
    name            text        NOT NULL DEFAULT '',
    gender          text        NOT NULL DEFAULT '?' CHECK (gender IN ('M', 'F', '?')),
    source_asset_id bigint,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- Daftar rig dipaginasi terbaru dulu dengan cursor keyset (created_at, template_id).
CREATE INDEX body_template_recent_idx ON body_template (created_at DESC, template_id);

-- --- Outfit -----------------------------------------------------------------

CREATE TABLE outfit (
    outfit_id text PRIMARY KEY,
    -- Nilai yang dikirim ke RecommendationService:RegisterItemAsync.
    reference_id uuid        NOT NULL UNIQUE,
    -- itemId balasan RegisterItemAsync; dibutuhkan RemoveItemAsync dan
    -- UpdateItemAsync, jadi wajib disimpan walau boleh kosong di awal.
    reco_item_id text,
    user_id      bigint      NOT NULL REFERENCES player (user_id),
    template_id  text        NOT NULL REFERENCES body_template (template_id),
    name         text        NOT NULL,
    is_public    boolean     NOT NULL DEFAULT false,
    custom_tags  jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- Warna kulit dan skala tubuh dari HumanoidDescription: {"colors": {...},
    -- "scales": {...}}. Item saja tidak cukup untuk merender ulang avatar.
    --
    -- Disimpan sebagai satu jsonb, bukan dipecah jadi dua belas kolom: isinya
    -- blob render yang dilaporkan klien dan dikembalikan apa adanya, tidak
    -- pernah dipakai untuk menyaring maupun mengurutkan. NULL berarti klien
    -- tidak melaporkannya.
    body         jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- Soft delete: baris tidak pernah dihapus supaya referenceId yang sudah
    -- beredar di feed tetap bisa dijelaskan (410, bukan 404).
    deleted_at   timestamptz,
    -- Embedding nama untuk pencarian makna lintas bahasa. NULL berarti belum
    -- (atau tidak akan) di-embed — pencarian tetap jalan lewat jalur trigram,
    -- jadi kolom ini murni peningkatan, bukan ketergantungan. Dimensi 1536
    -- mengikuti model text-embedding-3-small; ganti model = re-embed semua.
    name_embedding vector(1536)
);

-- Daftar outfit pemain memakai cursor keyset (updated_at desc, outfit_id).
CREATE INDEX outfit_user_recent_idx
    ON outfit (user_id, updated_at DESC, outfit_id)
    WHERE deleted_at IS NULL;

-- userId opsional pada GET /v1/outfits, jadi daftar lintas pemain butuh
-- indeksnya sendiri — tanpa ini query gabungan jatuh ke seq scan.
CREATE INDEX outfit_recent_idx
    ON outfit (updated_at DESC, outfit_id)
    WHERE deleted_at IS NULL;

-- Pencarian nama toleran salah ketik lewat operator trigram (<%).
CREATE INDEX outfit_name_trgm_idx
    ON outfit USING gin (name gin_trgm_ops);

-- Pencarian tetangga terdekat cosine di embedding nama. Parsial: baris yang
-- belum di-embed tidak membebani index.
CREATE INDEX outfit_name_hnsw_idx
    ON outfit USING hnsw (name_embedding vector_cosine_ops)
    WHERE deleted_at IS NULL AND name_embedding IS NOT NULL;

-- Detail item disimpan di sini, bukan di tabel katalog terpisah. Nilainya
-- dilaporkan klien dari AvatarEditorService dan dipakai apa adanya untuk
-- merender: backend tidak memverifikasinya ke Roblox.
--
-- Kolom teks boleh kosong karena klien tidak wajib melaporkan semuanya —
-- assetId dan slot sudah cukup untuk memasang item.
CREATE TABLE outfit_item (
    outfit_id  text    NOT NULL REFERENCES outfit (outfit_id) ON DELETE CASCADE,
    asset_id   bigint  NOT NULL,
    slot       text    NOT NULL,
    name       text    NOT NULL DEFAULT '',
    asset_type text    NOT NULL DEFAULT '',
    price      integer NOT NULL DEFAULT 0 CHECK (price >= 0),
    PRIMARY KEY (outfit_id, asset_id)
);

-- Satu slot boleh diisi lebih dari satu asset — indeksnya biasa, bukan unik.
-- Yang tetap ditolak adalah asset yang sama dua kali dalam satu outfit, lewat
-- primary key di atas. Indeks ini melayani ORDER BY slot saat item dibaca.
CREATE INDEX outfit_item_slot_idx ON outfit_item (outfit_id, slot);

-- --- Transaksi --------------------------------------------------------------

CREATE TABLE transaction (
    tx_id      text PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES player (user_id),
    universe_id bigint,
    place_id    bigint,
    job_id      text,
    -- Kunci idempotensi dibangkitkan di sisi Roblox sebelum permintaan
    -- pertama. Batasan unik inilah yang membuat retry setelah timeout tidak
    -- menghasilkan baris ganda.
    idempotency_key text        NOT NULL UNIQUE,
    status          text        NOT NULL
                    CHECK (status IN ('success', 'partial', 'failed', 'aborted')),
    occurred_at     timestamptz NOT NULL,
    received_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX transaction_user_recent_idx ON transaction (user_id, occurred_at DESC, tx_id);

-- asset_id tanpa foreign key: tidak ada lagi tabel asset yang bisa ditunjuk,
-- dan transaksi memang mencatat apa yang terjadi di Roblox apa adanya.
CREATE TABLE transaction_item (
    tx_id    text    NOT NULL REFERENCES transaction (tx_id) ON DELETE CASCADE,
    asset_id bigint  NOT NULL,
    price    integer NOT NULL DEFAULT 0 CHECK (price >= 0),
    result   text    NOT NULL CHECK (result IN ('success', 'aborted', 'failed')),
    PRIMARY KEY (tx_id, asset_id)
);

-- Catatan: respons idempoten untuk POST outfit disimpan di Redis, bukan di sini.
-- Kuncinya berumur pendek dan kedaluwarsanya ditangani Redis, jadi tidak perlu
-- tabel plus job pembersih. Yang tetap di database adalah
-- transaction.idempotency_key, karena itu batasan domain, bukan cache.

COMMIT;
