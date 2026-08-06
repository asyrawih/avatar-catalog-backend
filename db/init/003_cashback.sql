-- Skema cashback Robux.
--
-- Cashback dihitung dari TRANSACTION + TRANSACTION_ITEM (item result='success')
-- dan dicatat sebagai ledger append-only. Saldo pemain adalah SUM(amount) atas
-- seluruh barisnya — tidak ada kolom saldo yang bisa berubah diam-diam, dan
-- baris lama tidak pernah di-update, sesuai kebutuhan rekonsiliasi.
--
-- Semua angka dalam Robux. Konversi ke mata uang apa pun terjadi di luar
-- sistem ini.
--
-- Skrip ini dijalankan Postgres hanya saat volume data masih kosong. Untuk
-- database yang sudah berisi data, jalankan manual: psql -f 003_cashback.sql.

BEGIN;

-- Ledger cashback. amount bertanda: accrual positif, reversal (refund/
-- chargeback) dan redeem negatif, redeem_return positif (pengembalian saat
-- request ditolak). Saldo boleh minus setelah reversal — itu disengaja.
--
-- spend dan rate_percent hanya bermakna pada accrual; keduanya disimpan supaya
-- setiap baris bisa dijelaskan tanpa menghitung ulang bonus yang berlaku saat
-- itu (jadwal event dan riwayat streak berubah seiring waktu).
CREATE TABLE cashback_entry (
    entry_id     text PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES player (user_id),
    tx_id        text        REFERENCES transaction (tx_id),
    request_id   text,
    kind         text        NOT NULL
                 CHECK (kind IN ('accrual', 'reversal', 'redeem', 'redeem_return')),
    spend        integer     NOT NULL DEFAULT 0 CHECK (spend >= 0),
    rate_percent integer     NOT NULL DEFAULT 0 CHECK (rate_percent >= 0),
    amount       integer     NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Dedup: satu transaksi hanya boleh punya satu accrual dan satu reversal.
-- Batasan unik inilah yang membuat retry POST tidak menggandakan cashback.
CREATE UNIQUE INDEX cashback_entry_tx_kind_key
    ON cashback_entry (tx_id, kind)
    WHERE tx_id IS NOT NULL;

-- Ledger per pemain dipaginasi terbaru dulu dengan cursor keyset.
CREATE INDEX cashback_entry_user_recent_idx
    ON cashback_entry (user_id, created_at DESC, entry_id);

-- Rekonsiliasi menjumlahkan per rentang waktu lintas pemain.
CREATE INDEX cashback_entry_created_idx ON cashback_entry (created_at);

-- Request redeem. Saldo dipotong saat request dibuat (baris kind='redeem' di
-- ledger); fulfillment dikerjakan tim internal di luar sistem, game hanya
-- menerima update status.
CREATE TABLE redeem_request (
    request_id   text        PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES player (user_id),
    amount       integer     NOT NULL CHECK (amount > 0),
    status       text        NOT NULL
                 CHECK (status IN ('pending', 'completed', 'rejected')),
    requested_at timestamptz NOT NULL DEFAULT now(),
    resolved_at  timestamptz
);

-- Rate limit dari spec: maksimal satu request pending per pemain. Ditegakkan
-- database, bukan pemeriksaan baca-lalu-tulis yang bisa balapan.
CREATE UNIQUE INDEX redeem_request_pending_key
    ON redeem_request (user_id)
    WHERE status = 'pending';

CREATE INDEX redeem_request_user_recent_idx
    ON redeem_request (user_id, requested_at DESC, request_id);

-- Jadwal event bonus. Bonus event aktif bila now() jatuh di dalam salah satu
-- jendela; jendela boleh tumpang tindih, bonusnya tetap satu kali.
CREATE TABLE cashback_event (
    event_id  text        PRIMARY KEY,
    name      text        NOT NULL DEFAULT '',
    starts_at timestamptz NOT NULL,
    ends_at   timestamptz NOT NULL,
    CHECK (ends_at > starts_at)
);

CREATE INDEX cashback_event_window_idx ON cashback_event (starts_at, ends_at);

COMMIT;
