-- Kunci API untuk konsumen backend: Roblox game server, dashboard internal,
-- pengambil data latih AI, dan nanti mungkin API publik.
--
-- Yang tersimpan di sini HASH-nya saja, bukan tokennya. Konsekuensinya token
-- yang hilang tidak bisa dipulihkan — hanya bisa diterbitkan ulang — dan itu
-- memang yang diinginkan: dump database, backup, atau kebocoran log tidak
-- memberi penyerang satu pun token yang bisa dipakai.
--
-- Skrip ini dijalankan Postgres hanya saat volume masih kosong. Untuk database
-- yang sudah berisi, jalankan sendiri:
--   psql "$DATABASE_URL" -f db/init/005_api_key.sql
-- Semua pernyataan di bawah aman diulang.

CREATE TABLE IF NOT EXISTS api_key (
    -- Ikut di dalam token dan boleh dianggap publik. Ada supaya verifikasi
    -- cukup satu lookup indeks, bukan memindai seluruh tabel lalu
    -- membandingkan satu per satu.
    key_id     text PRIMARY KEY,
    -- SHA-256 dari token utuh. Bukan bcrypt/argon2: keduanya untuk rahasia
    -- berentropi rendah seperti kata sandi manusia. Token di sini 256 bit
    -- acak, jadi memperlambat verifikasi hanya menambah latensi tiap request
    -- tanpa menambah keamanan.
    token_hash bytea NOT NULL,
    -- Nama yang bisa dibaca manusia, mis. "roblox-game-server-prod".
    -- Muncul di log akses, jadi saat ada yang aneh langsung ketahuan kunci
    -- milik siapa.
    name       text NOT NULL,
    -- Izin. Disimpan sebagai array teks, bukan tabel penghubung: daftarnya
    -- pendek, selalu dibaca utuh bersama barisnya, dan tidak pernah dicari
    -- terbalik ("kunci mana saja yang punya scope X" bukan pertanyaan yang
    -- dipakai jalur request).
    scopes     text[] NOT NULL CHECK (cardinality(scopes) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    -- NULL = tanpa masa berlaku. Kunci untuk pihak luar sebaiknya selalu
    -- diberi batas; kunci tanpa batas adalah kunci yang tidak pernah dirotasi.
    expires_at timestamptz,
    -- Pencabutan tidak menghapus baris: nama dan waktu pakai terakhirnya masih
    -- dibutuhkan saat menelusuri insiden.
    revoked_at timestamptz,
    -- Diperbarui paling sering sekali per menit per kunci, bukan tiap request
    -- (lihat internal/store/postgres/apikeys.go). Cukup untuk menjawab "kunci
    -- ini masih dipakai atau sudah bisa dicabut?" tanpa menulis ke tabel yang
    -- sama pada setiap panggilan API.
    last_used_at timestamptz
);

-- Menemukan kunci yang masih hidup saat meninjau akses.
CREATE INDEX IF NOT EXISTS api_key_active_idx ON api_key (created_at DESC)
    WHERE revoked_at IS NULL;
