-- Migrasi sekali jalan: menambah OUTFIT.body.
--
-- Dipakai untuk database yang sudah terlanjur dibuat tanpa kolom ini. Database
-- baru tidak butuh ini — db/init/001_schema.sql sudah berbentuk akhir.
--
-- Kolomnya nullable tanpa default: outfit lama memang tidak pernah melaporkan
-- warna dan skala, dan NULL adalah cara jujur mengatakannya. Mengisi dengan
-- nilai karangan akan membuat klien merender avatar yang bukan milik pemainnya.
--
--   docker compose exec -T db psql -U avatar -d avatar_catalog -v ON_ERROR_STOP=1 < db/migrate_add_outfit_body.sql

BEGIN;

ALTER TABLE outfit ADD COLUMN IF NOT EXISTS body jsonb;

COMMIT;
