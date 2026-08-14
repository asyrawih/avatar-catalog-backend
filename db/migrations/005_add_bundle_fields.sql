-- Migrasi: field bundle dan thumbnail.
--
-- Game server kini melaporkan bagian paket (bundle) dengan price = harga
-- bundle induk yang TERULANG di tiap bagiannya, plus bundleId/bundleName —
-- dan thumbnailAssetId di level outfit. Pembaca yang menjumlahkan harga wajib
-- menghitung per bundle_id sekali; perhitungan cashback sudah dikoreksi ikut
-- aturan itu.
--
-- Idempoten dan aman dijalankan pada database mana pun (baru maupun lama) —
-- pada database baru db/init/001_schema.sql sudah berbentuk akhir, jadi setiap
-- pernyataan di sini jadi no-op. Diterapkan otomatis oleh Job
-- avatar-catalog-db-migrate (lihat k8s/base/migrate-job.yaml) sebelum setiap
-- rollout api; tidak perlu dijalankan manual.

BEGIN;

ALTER TABLE outfit ADD COLUMN IF NOT EXISTS thumbnail_asset_id bigint;

ALTER TABLE outfit_item ADD COLUMN IF NOT EXISTS bundle_id bigint;
ALTER TABLE outfit_item ADD COLUMN IF NOT EXISTS bundle_name text NOT NULL DEFAULT '';

ALTER TABLE transaction_item ADD COLUMN IF NOT EXISTS bundle_id bigint;

COMMIT;
