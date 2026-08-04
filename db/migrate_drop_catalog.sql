-- Migrasi sekali jalan: melepas katalog dari database yang sudah berisi data.
--
-- Dipakai untuk database yang sudah terlanjur dibuat dengan skema lama. Database
-- baru tidak butuh ini — db/init/001_schema.sql sudah berbentuk akhir.
--
-- Detail item dipindahkan dari CATALOG_ITEM ke OUTFIT_ITEM lebih dulu, jadi
-- outfit yang sudah ada tidak kehilangan nama, tipe, dan harga itemnya.
--
--   docker compose exec -T db psql -U avatar -d avatar_catalog -v ON_ERROR_STOP=1 < db/migrate_drop_catalog.sql

BEGIN;

-- 1. Kolom detail menempel di OUTFIT_ITEM.
ALTER TABLE outfit_item
    ADD COLUMN IF NOT EXISTS name       text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS asset_type text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS price      integer NOT NULL DEFAULT 0;

-- 2. Pindahkan detail yang sudah tercatat di katalog. Baris katalog yang mati
--    atau termoderasi ikut dipindah apa adanya: statusnya memang hilang, tapi
--    nama terakhir yang diketahui lebih berguna daripada kolom kosong.
UPDATE outfit_item oi
SET name       = ci.name,
    asset_type = ci.asset_type,
    price      = ci.price
FROM catalog_item ci
WHERE ci.asset_id = oi.asset_id;

ALTER TABLE outfit_item
    ADD CONSTRAINT outfit_item_price_check CHECK (price >= 0);

-- 3. Lepas foreign key ke katalog. Nama constraint dibangkitkan Postgres saat
--    tabel dibuat, jadi dicari lewat katalog sistem alih-alih ditebak.
DO $$
DECLARE
    fk record;
BEGIN
    FOR fk IN
        SELECT con.conrelid::regclass AS tbl, con.conname AS name
        FROM pg_constraint con
        WHERE con.contype = 'f'
          AND con.confrelid = 'catalog_item'::regclass
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', fk.tbl, fk.name);
    END LOOP;
END $$;

-- 4. Buang tabel katalog. CASCADE menyapu section_item dan bundle_item yang
--    memang tidak punya arti tanpa induknya.
DROP TABLE IF EXISTS section_item, section, bundle_item, bundle, sync_run,
                     catalog_item, creator CASCADE;

COMMIT;
