-- Migrasi sekali jalan: melepas batasan satu-slot-satu-asset.
--
-- Dipakai untuk database yang sudah terlanjur dibuat dengan indeks unik lama.
-- Database baru tidak butuh ini — db/init/001_schema.sql sudah berbentuk akhir.
--
--   docker compose exec -T db psql -U avatar -d avatar_catalog -v ON_ERROR_STOP=1 < db/migrate_drop_slot_unique.sql

BEGIN;

DROP INDEX IF EXISTS outfit_item_slot_idx;

-- Indeksnya tetap ada, cuma tidak lagi unik: ORDER BY slot saat membaca item
-- masih memakainya.
CREATE INDEX IF NOT EXISTS outfit_item_slot_idx ON outfit_item (outfit_id, slot);

COMMIT;
