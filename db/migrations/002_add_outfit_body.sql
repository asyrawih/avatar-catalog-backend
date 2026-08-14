-- Migrasi: menambah OUTFIT.body.
--
-- Idempoten dan aman dijalankan pada database mana pun (baru maupun lama) —
-- pada database baru db/init/001_schema.sql sudah berbentuk akhir, jadi setiap
-- pernyataan di sini jadi no-op. Diterapkan otomatis oleh Job
-- avatar-catalog-db-migrate (lihat k8s/base/migrate-job.yaml) sebelum setiap
-- rollout api; tidak perlu dijalankan manual.
--
-- Kolomnya nullable tanpa default: outfit lama memang tidak pernah melaporkan
-- warna dan skala, dan NULL adalah cara jujur mengatakannya. Mengisi dengan
-- nilai karangan akan membuat klien merender avatar yang bukan milik pemainnya.

BEGIN;

ALTER TABLE outfit ADD COLUMN IF NOT EXISTS body jsonb;

COMMIT;
