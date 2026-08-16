-- Login untuk dashboard internal: operator manusia, bukan konsumen mesin.
--
-- Kenapa terpisah dari api_key dan tidak menumpang tabel itu: kredensialnya
-- beda jenis. Token kunci API 256 bit acak dan cukup di-SHA-256; kata sandi
-- manusia berentropi rendah dan wajib melewati KDF yang mahal (argon2id di
-- internal/auth). Menyatukan keduanya berarti satu kolom hash dengan dua
-- aturan verifikasi, dan cepat atau lambat ada jalur yang memakai aturan yang
-- salah.
--
-- Skrip ini dijalankan Postgres hanya saat volume masih kosong. Untuk database
-- yang sudah berisi, jalankan sendiri:
--   psql "$DATABASE_URL" -f db/init/006_dashboard_user.sql
-- Semua pernyataan di bawah aman diulang.

CREATE TABLE IF NOT EXISTS dashboard_user (
    user_id       text PRIMARY KEY,
    -- Email sekaligus nama login. Disimpan huruf kecil supaya pencarian saat
    -- login tidak bergantung pada cara pengguna mengetiknya.
    email         text NOT NULL UNIQUE,
    -- Encoded hash argon2id lengkap dengan parameter dan salt-nya, format
    -- $argon2id$v=19$m=...,t=...,p=...$salt$hash. Parameternya ikut tersimpan
    -- supaya hash lama tetap bisa diverifikasi setelah biayanya dinaikkan.
    password_hash text NOT NULL,
    name          text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- Bukan nil = tidak bisa login lagi. Barisnya sengaja tidak dihapus:
    -- riwayat siapa yang dulu punya akses justru yang dibutuhkan saat
    -- menelusuri insiden.
    disabled_at   timestamptz,
    last_login_at timestamptz
);

-- Sesi login. Yang tersimpan hash-nya, sama alasannya dengan api_key: dump
-- database tidak boleh memberi penyerang satu pun sesi yang bisa dipakai.
--
-- Sesi disimpan di Postgres, bukan JWT tanpa state: logout harus benar-benar
-- mematikan sesi, dan menonaktifkan operator harus langsung berlaku. Keduanya
-- mustahil dengan token yang hanya diverifikasi tanda tangannya.
CREATE TABLE IF NOT EXISTS dashboard_session (
    session_id text PRIMARY KEY,
    token_hash bytea NOT NULL,
    user_id    text NOT NULL REFERENCES dashboard_user (user_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    -- Untuk peninjauan: dari mana sesi ini dipakai terakhir kali.
    last_seen_at timestamptz,
    user_agent   text NOT NULL DEFAULT ''
);

-- Menghapus sesi kedaluwarsa dan mencari sesi aktif milik satu user keduanya
-- memindai kolom yang sama.
CREATE INDEX IF NOT EXISTS dashboard_session_user_idx
    ON dashboard_session (user_id, expires_at DESC)
    WHERE revoked_at IS NULL;
