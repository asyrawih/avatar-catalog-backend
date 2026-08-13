-- Migrasi sekali jalan: menambah OUTFIT_ITEM.adjust.
--
-- Klien kini melaporkan koreksi penempatan per item ({pos, rot, scale}), dan
-- backend menyimpannya apa adanya supaya hasil GET bisa dikirim balik untuk
-- merender avatar persis seperti yang disimpan pemainnya.
--
-- Dipakai untuk database yang sudah terlanjur dibuat tanpa kolom ini. Database
-- baru tidak butuh ini — db/init/001_schema.sql sudah berbentuk akhir.
--
-- Kolomnya nullable tanpa default: item lama memang tidak pernah melaporkan
-- koreksi, dan NULL adalah cara jujur mengatakannya. Mengisi dengan nol akan
-- berarti "geser ke nol" — sebuah koreksi yang tidak pernah diminta siapa pun.
--
--   docker compose exec -T db psql -U avatar -d avatar_catalog -v ON_ERROR_STOP=1 < db/migrate_add_item_adjust.sql

BEGIN;

ALTER TABLE outfit_item ADD COLUMN IF NOT EXISTS adjust jsonb;

COMMIT;
