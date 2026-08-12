-- Like dan view outfit.
--
-- Dua lapis, sengaja:
--
--   * outfit_like / outfit_view  = log kejadian per pemain. Ini bahan mentah
--     untuk melatih generator outfit nanti — model butuh tahu SIAPA menyukai
--     APA, bukan cuma angka totalnya. Sekali angka diringkas jadi counter,
--     sinyal itu hilang selamanya dan tidak bisa direkonstruksi.
--   * outfit.like_count / view_count = ringkasan yang ikut terbawa di setiap
--     baris daftar. Tanpa ini GET /v1/outfits harus meng-COUNT dua tabel per
--     outfit di tiap request.
--
-- Counter diperbarui di transaksi yang sama dengan penulisan log, jadi
-- keduanya tidak bisa berpisah. Angka yang benar tetap yang di tabel log;
-- kalau counter dicurigai melenceng, lihat query rekonsiliasi di bawah.
--
-- Skrip ini dijalankan Postgres hanya saat volume masih kosong. Untuk
-- database yang sudah berisi, jalankan sendiri:
--   psql "$DATABASE_URL" -f db/init/004_engagement.sql
-- Semua pernyataan di bawah aman diulang.

ALTER TABLE outfit
    ADD COLUMN IF NOT EXISTS like_count integer NOT NULL DEFAULT 0 CHECK (like_count >= 0),
    ADD COLUMN IF NOT EXISTS view_count integer NOT NULL DEFAULT 0 CHECK (view_count >= 0);

-- Satu pemain satu like per outfit: PK-nya yang menegakkan, bukan kode
-- aplikasi. Like berulang jadi no-op, bukan penggandaan angka.
CREATE TABLE IF NOT EXISTS outfit_like (
    outfit_id  text        NOT NULL REFERENCES outfit (outfit_id) ON DELETE CASCADE,
    user_id    bigint      NOT NULL REFERENCES player (user_id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (outfit_id, user_id)
);

-- Melihat "outfit apa saja yang disukai pemain X" — arah baca yang dipakai
-- saat menyiapkan data latih per pemain.
CREATE INDEX IF NOT EXISTS outfit_like_user_idx ON outfit_like (user_id, created_at DESC);

-- View bersifat append-only: satu baris per kejadian, tanpa kunci unik.
-- Pemain yang membuka outfit sama lima kali memang lima sinyal, bukan satu —
-- dan berapa kali dilihat sebelum disukai adalah bagian dari sinyalnya.
--
-- Konsekuensinya tabel ini tumbuh paling cepat di seluruh skema. Kalau nanti
-- terlalu besar, ringkas per hari ke tabel agregat lalu buang barisnya;
-- jangan hapus tanpa meringkas.
CREATE TABLE IF NOT EXISTS outfit_view (
    view_id   bigserial   PRIMARY KEY,
    outfit_id text        NOT NULL REFERENCES outfit (outfit_id) ON DELETE CASCADE,
    -- NULL = penonton anonim (belum ada aktor di request). Tetap dicatat
    -- karena masih berguna untuk popularitas, walau tidak untuk training
    -- per-pemain.
    user_id   bigint      REFERENCES player (user_id),
    viewed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS outfit_view_outfit_idx ON outfit_view (outfit_id, viewed_at DESC);
CREATE INDEX IF NOT EXISTS outfit_view_user_idx   ON outfit_view (user_id, viewed_at DESC)
    WHERE user_id IS NOT NULL;

-- Indeks pengurutan populer. Kolom kedua outfit_id mengikuti keyset cursor
-- (like_count DESC, outfit_id ASC) supaya paginasi tidak melompat; parsial
-- pada baris hidup karena daftar tidak pernah menampilkan yang terhapus.
CREATE INDEX IF NOT EXISTS outfit_like_count_idx ON outfit (like_count DESC, outfit_id ASC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS outfit_view_count_idx ON outfit (view_count DESC, outfit_id ASC)
    WHERE deleted_at IS NULL;

-- Rekonsiliasi: menyamakan counter dengan tabel log. Aman dijalankan kapan
-- saja; hasilnya nol baris berubah kalau semuanya sudah benar.
--
--   UPDATE outfit o SET
--       like_count = (SELECT count(*) FROM outfit_like l WHERE l.outfit_id = o.outfit_id),
--       view_count = (SELECT count(*) FROM outfit_view v WHERE v.outfit_id = o.outfit_id)
--   WHERE like_count <> (SELECT count(*) FROM outfit_like l WHERE l.outfit_id = o.outfit_id)
--      OR view_count <> (SELECT count(*) FROM outfit_view v WHERE v.outfit_id = o.outfit_id);
