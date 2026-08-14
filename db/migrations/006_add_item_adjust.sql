-- Migrasi: menambah OUTFIT_ITEM.adjust.
--
-- Klien kini melaporkan koreksi penempatan per item ({pos, rot, scale}), dan
-- backend menyimpannya apa adanya supaya hasil GET bisa dikirim balik untuk
-- merender avatar persis seperti yang disimpan pemainnya.
--
-- Idempoten dan aman dijalankan pada database mana pun (baru maupun lama) —
-- pada database baru db/init/001_schema.sql sudah berbentuk akhir, jadi setiap
-- pernyataan di sini jadi no-op. Diterapkan otomatis oleh Job
-- avatar-catalog-db-migrate (lihat k8s/base/migrate-job.yaml) sebelum setiap
-- rollout api; tidak perlu dijalankan manual.
--
-- Kolomnya nullable tanpa default: item lama memang tidak pernah melaporkan
-- koreksi, dan NULL adalah cara jujur mengatakannya. Mengisi dengan nol akan
-- berarti "geser ke nol" — sebuah koreksi yang tidak pernah diminta siapa pun.

BEGIN;

ALTER TABLE outfit_item ADD COLUMN IF NOT EXISTS adjust jsonb;

COMMIT;
