# db/migrations

Perubahan skema untuk database yang **sudah berisi data**. Dijalankan otomatis
sebelum api start (initContainer di Kubernetes, service `migrate` di compose),
berurutan, sekali saja per database — catatannya ada di tabel
`schema_migrations`.

## Menulis migrasi baru

Nama berkas: `NNNN_deskripsi_singkat.sql`, nomor empat digit berurutan.

```sql
-- Migrasi 0001: menambah OUTFIT.body.
--
-- Kolomnya nullable tanpa default: outfit lama memang tidak pernah melaporkan
-- warna dan skala, dan NULL adalah cara jujur mengatakannya.

ALTER TABLE outfit ADD COLUMN IF NOT EXISTS body jsonb;
```

Aturannya:

- **Jangan tulis `BEGIN;`/`COMMIT;`.** Runner membungkus tiap berkas dalam satu
  transaksi sendiri — migrasi yang gagal di tengah tidak meninggalkan setengah
  perubahan, dan barisnya tidak masuk `schema_migrations`.
- Untuk perintah yang tidak boleh ada di dalam transaksi (mis.
  `CREATE INDEX CONCURRENTLY`), tulis `-- migrate: no-transaction` di baris
  paling atas berkas. Kegagalan di tengah berkas seperti itu harus dibereskan
  manual.
- Tulis idempoten (`IF NOT EXISTS`, `IF EXISTS`) kalau bisa. Runner sudah
  menjaga tiap berkas jalan sekali, tapi idempotensi menyelamatkan saat
  database dipulihkan dari backup lama.
- **Berkas yang sudah pernah dijalankan jangan diedit lagi.** Checksum-nya
  ikut tercatat; kalau isinya berubah, runner berhenti dengan pesan drift
  alih-alih diam-diam melewatkannya. Perbaikan ditulis sebagai migrasi baru.
- Ubah juga `db/init/001_schema.sql` supaya database **baru** langsung lahir
  dengan bentuk akhir. Berkas di `db/init` hanya dieksekusi Postgres saat PGDATA
  masih kosong.

## Database baru

`db/init/*.sql` sudah berbentuk akhir, jadi migrasi di sini akan menemukan
kolomnya sudah ada. Supaya tidak dijalankan percuma, `migrate up` pada database
yang **baru saja** dibuat sebaiknya didahului:

```bash
migrate baseline   # tandai semua migrasi saat ini sebagai sudah diterapkan
```

Ini juga yang dipakai sekali di database produksi yang sudah lama jalan, saat
tabel `schema_migrations` pertama kali muncul.

## Sebelum mekanisme ini ada

`db/migrate_*.sql` di direktori induk adalah migrasi satu kali gaya lama yang
diterapkan manual. Berkas-berkas itu ditinggalkan apa adanya sebagai catatan
sejarah — jangan dipindahkan ke sini, karena database yang ada sudah
menjalankannya dan `db/init/001_schema.sql` sudah memuat hasilnya.
