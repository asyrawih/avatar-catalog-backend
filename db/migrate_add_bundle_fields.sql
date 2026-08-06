-- Migrasi sekali jalan: field bundle dan thumbnail.
--
-- Game server kini melaporkan bagian paket (bundle) dengan price = harga
-- bundle induk yang TERULANG di tiap bagiannya, plus bundleId/bundleName —
-- dan thumbnailAssetId di level outfit. Pembaca yang menjumlahkan harga wajib
-- menghitung per bundle_id sekali; perhitungan cashback sudah dikoreksi ikut
-- aturan itu.
--
-- Dipakai untuk database yang sudah terlanjur dibuat tanpa kolom ini. Database
-- baru tidak butuh ini — db/init/001_schema.sql sudah berbentuk akhir.
--
--   docker compose exec -T db psql -U avatar -d avatar_catalog -v ON_ERROR_STOP=1 < db/migrate_add_bundle_fields.sql

BEGIN;

ALTER TABLE outfit ADD COLUMN IF NOT EXISTS thumbnail_asset_id bigint;

ALTER TABLE outfit_item ADD COLUMN IF NOT EXISTS bundle_id bigint;
ALTER TABLE outfit_item ADD COLUMN IF NOT EXISTS bundle_name text NOT NULL DEFAULT '';

ALTER TABLE transaction_item ADD COLUMN IF NOT EXISTS bundle_id bigint;

COMMIT;
