-- Migrasi: melepas batasan satu-slot-satu-asset.
--
-- Idempoten dan aman dijalankan pada database mana pun (baru maupun lama) —
-- pada database baru db/init/001_schema.sql sudah berbentuk akhir, jadi setiap
-- pernyataan di sini jadi no-op. Diterapkan otomatis oleh Job
-- avatar-catalog-db-migrate (lihat k8s/base/migrate-job.yaml) sebelum setiap
-- rollout api; tidak perlu dijalankan manual.

BEGIN;

DROP INDEX IF EXISTS outfit_item_slot_idx;

-- Indeksnya tetap ada, cuma tidak lagi unik: ORDER BY slot saat membaca item
-- masih memakainya.
CREATE INDEX IF NOT EXISTS outfit_item_slot_idx ON outfit_item (outfit_id, slot);

COMMIT;
