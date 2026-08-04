# Avatar Catalog Backend

Backend katalog avatar Roblox dalam Go. Bentuk API mengikuti collection Postman
`avatar-catalog-api`, dan skemanya mengikuti ERD v3 di FigJam (13 tabel).

Autentikasi sengaja ditunda — lihat [Autentikasi](#autentikasi-belum-dikerjakan).

## Menjalankan

```bash
docker compose up -d --build
```

Cara ini menjalankan API bersama Postgres dan Redis — lihat [Docker](#docker).

Untuk uji cepat tanpa docker, server juga bisa jalan tanpa dependensi apa pun:

```bash
go run ./cmd/server
```

Tanpa `DATABASE_URL` penyimpanan memakai in-memory dan otomatis memuat data
contoh yang sama dengan mock Postman; tanpa `REDIS_URL` cache dimatikan. Datanya
hilang saat proses berhenti.

```bash
curl "http://localhost:8080/v1/outfits?userId=627278822"
```

Perintah lain:

```bash
go test ./...
```

Test integrasi Postgres dilewati kecuali `TEST_DATABASE_URL` diisi. Test ini
mengosongkan tabel, jadi arahkan hanya ke database sekali pakai:

```bash
make test-integration
```

## Docker

```bash
docker compose up -d --build
```

Menjalankan tiga service: `api` di `:8080`, `db` (Postgres 17) di `:5432`, dan
`redis` (Redis 8) di `:6379`. `api` menunggu healthcheck keduanya lulus sebelum
start. Tambahkan `--profile tools` kalau butuh Adminer di `:8081`.

```bash
curl http://localhost:8080/readyz
```

`/readyz` melaporkan status tiap dependensi dan menjawab `503` kalau salah satu
bermasalah; `/healthz` hanya membuktikan prosesnya hidup.

Kredensial bawaan ada di [.env.example](.env.example) — salin ke `.env` kalau
mau menggantinya. Password bawaan hanya untuk pengembangan lokal. Kalau port
`5432` atau `6379` sudah dipakai proyek lain, ganti `POSTGRES_PORT` /
`REDIS_PORT` di `.env`; `api` tetap memanggil `db:5432` dan `redis:6379` lewat
jaringan compose, jadi hanya akses dari host yang berubah.

### Skema

[db/init/001_schema.sql](db/init/001_schema.sql) berisi 13 tabel ERD v3, dan
[db/init/002_seed.sql](db/init/002_seed.sql) mengisi data contoh yang sama
dengan mock Postman. Keduanya dijalankan Postgres **hanya saat volume masih
kosong**, jadi setelah mengubah skema jalankan:

```bash
docker compose down -v && docker compose up -d db
```

Beberapa aturan API sudah ditegakkan di tingkat skema, bukan hanya di kode:
`transaction.idempotency_key` unik (retry tidak bisa membuat baris ganda),
`outfit.reference_id` unik, indeks unik `(outfit_id, slot)` pada `outfit_item`
(pasangan basis data dari `409 duplicate_slot`), dan `catalog_item.status`
dibatasi `active|dead|moderated`.

`outfit.user_id` dan `transaction.user_id` menunjuk tabel `player`. Backend
belum punya sumber kebenaran untuk username, jadi adapter Postgres menyisipkan
baris pemain minimal (`INSERT ... ON CONFLICT DO NOTHING`) di transaksi yang
sama saat outfit atau transaksi pertamanya masuk. `body_template` diperlakukan
sama — lihat [Rig](#rig-templateid).

Data contoh hanya berisi **satu** rig (`88484288792766`, development item) dan
kedua outfit contoh memakainya, karena itu satu-satunya asset id rig yang benar
nyata. Rig lain tidak dikarang di seed; begitu dipakai, barisnya muncul sendiri.

## Cache

Dengan `REDIS_URL` terisi, jalur baca yang paling sering dipukul game server
dibungkus cache: isi etalase, metadata etalase, pencarian item katalog,
penerbit, detail outfit, dan daftar outfit per pemain. Tanpa `REDIS_URL` semua
tetap berjalan, hanya langsung ke database.

Pembatalan cache memakai **versi namespace**, bukan penghapusan per pola:
setiap penulisan menaikkan satu penghitung (`ver:catalog`, `ver:outfit:user:<id>`)
sehingga kunci lama tidak mungkin terbaca lagi dan hilang sendiri saat TTL
habis. Menghapus per pola berarti memindai seluruh keyspace Redis, yang justru
paling berat persis ketika katalog paling sering berubah.

Konsekuensinya:

- `POST /v1/catalog/sync-runs` yang menandai asset mati langsung menggugurkan
  seluruh cache katalog — item mati tidak akan bertahan di etalase sampai TTL
  habis.
- Perubahan outfit satu pemain hanya menggugurkan daftar milik pemain itu.
- `POST /v1/outfits/resolve` sengaja tidak di-cache: kombinasi `referenceId`
  dari feed hampir selalu berbeda, jadi entrinya nyaris tak pernah terpakai ulang.

Kegagalan Redis tidak pernah menjatuhkan permintaan — semua error cache
diperlakukan sebagai miss dan dilayani langsung dari database.

Kunci idempotensi untuk `POST /v1/outfits` dan `POST /v1/catalog/sync-runs`
juga disimpan di Redis (TTL `IDEMPOTENCY_TTL`). Idempotensi transaksi tetap di
Postgres lewat kolom unik, karena itu batasan domain yang harus bertahan walau
Redis dikosongkan.

## Endpoint

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/v1/outfits?userId=&isPublic=&limit=&cursor=` | Daftar outfit, terbaru dulu; `userId` opsional |
| POST | `/v1/outfits` | Buat outfit; backend membangkitkan `referenceId` |
| GET | `/v1/outfits/{outfitId}` | Detail outfit beserta item |
| PATCH | `/v1/outfits/{outfitId}` | Ubah sebagian metadata, termasuk simpan `recoItemId` |
| PUT | `/v1/outfits/{outfitId}/items` | Ganti seluruh isi item |
| DELETE | `/v1/outfits/{outfitId}` | Soft delete (isi `deletedAt`) |
| POST | `/v1/outfits/resolve` | Tukar sekumpulan `referenceId` jadi metadata render |
| GET | `/v1/templates?limit=&cursor=` | Daftar rig terdaftar, terbaru dulu |
| POST | `/v1/templates` | Daftarkan rig beserta nama dan gender |
| GET | `/v1/templates/{templateId}` | Detail satu rig |
| PATCH | `/v1/templates/{templateId}` | Isi nama dan gender rig |
| GET | `/v1/catalog/sections/{sectionId}/items` | Isi etalase, hanya item `active` |
| POST | `/v1/catalog/items:batch-get` | Ambil banyak item katalog sekaligus |
| POST | `/v1/catalog/sync-runs` | Laporan sync worker |
| POST | `/v1/transactions` | Catat transaksi (wajib `Idempotency-Key`) |
| GET | `/v1/transactions?userId=&limit=&cursor=` | Riwayat transaksi pemain |
| GET | `/healthz`, `/readyz` | Probe kesehatan |

## Rig (templateId)

`templateId` **adalah Roblox asset id** dari rig yang sudah di-upload, dikirim
sebagai digit — bukan slug internal. Alurnya: rig di-upload ke Roblox lebih
dulu, lalu asset id-nya ikut dalam `POST /v1/outfits`.

Karena Roblox yang memegang rig-nya, backend tidak menolak asset id yang belum
pernah dilihatnya: **rig terdaftar sendiri pada pemakaian pertama**, dengan
nama kosong dan gender `?`. Isi keduanya belakangan lewat
`PATCH /v1/templates/{templateId}`, atau daftarkan lengkap di depan lewat
`POST /v1/templates`. Rig yang sudah punya nama tidak pernah tertimpa oleh
pemakaian berikutnya.

Yang tetap ditolak adalah id yang bukan asset id sama sekali —
`422 invalid_template_id` untuk apa pun yang mengandung selain digit atau
diawali nol. Tanpa guard ini `body_template` akan terisi salah ketik dan FK
`OUTFIT.template_id` kehilangan artinya.

`templateId` boleh dikirim sebagai string (`"88484288792766"`) maupun angka
(`88484288792766`) — klien Luau yang menyimpannya sebagai number akan mengirim
angka telanjang lewat `JSONEncode`, dan keduanya diterima.

## Keputusan desain

**Idempotensi.** `POST /v1/transactions` mewajibkan header `Idempotency-Key`
dan menegakkannya lewat kolom unik `TRANSACTION.idempotencyKey` — bukan cache
respons — supaya tetap berlaku walau proses backend sempat restart. Pengulangan
dijawab `200` dengan `idempotentReplay: true`, bukan `201`.

`POST /v1/outfits` dan `POST /v1/catalog/sync-runs` memakai header yang sama
tapi lewat middleware penyimpan respons; header di sini opsional.

**Daftar outfit tanpa userId.** `GET /v1/outfits` bisa dipanggil tanpa `userId`
dan mengembalikan outfit semua pemain — berguna untuk feed dan untuk memeriksa
isi katalog saat pengembangan. Tambahkan `isPublic=true` untuk membatasinya ke
outfit publik saja.

Perhatikan: selama autentikasi belum terpasang, daftar tanpa `userId` **juga
menampilkan outfit privat milik semua pemain**. Begitu autentikasi masuk,
daftar gabungan ini sebaiknya dibatasi — hanya `isPublic=true`, atau hanya
untuk token layanan.

**Cursor, bukan offset.** Katalog berubah terus karena sync worker, jadi offset
akan menggeser hasil di antara dua permintaan. Cursor dikodekan base64url dan
buram bagi klien: daftar outfit dan transaksi memakai keyset `(waktu, id)`, isi
etalase memakai posisi karena urutannya stabil.

**Item mati.** Item ber-status `dead` tetap muncul di detail outfit tapi
ditandai (`status: "dead"`, `name`/`price` null) supaya game server bisa
melewatinya saat membangun `HumanoidDescription` tanpa membuat rig gagal render.
Sebaliknya, etalase menyaring habis item non-`active`: kalau item mati lolos ke
klien, checkout-nya akan gagal di sisi Roblox.

Menulis outfit baru dengan asset mati ditolak `422 dead_asset` — baik lewat
`POST /v1/outfits` maupun `PUT .../items`. Outfit lama boleh tetap menyimpannya
sebagai riwayat.

**Sync run.** Hanya error `404` yang dianggap "asset benar-benar hilang" lalu
ditandai mati. Status lain (429, 5xx) adalah kegagalan job, dan menandainya mati
akan menghapus item yang sehat. Laporan dibatasi per `source`
(bawaan 5 per menit) dan menjawab `429` dengan header `Retry-After`.

**Soft delete.** `DELETE` hanya mengisi `deletedAt`. Responsnya membawa
`recoItemId` sebagai pengingat memanggil `RemoveItemAsync` — kalau tidak,
outfit yang sudah dihapus tetap muncul di feed. `GET` outfit yang sudah dihapus
menjawab `410 outfit_deleted`, bukan `404`.

**Bentuk error.** Semua kegagalan memakai satu amplop:

```json
{ "error": { "code": "dead_asset", "message": "...", "details": [ ... ] } }
```

## Autentikasi (belum dikerjakan)

Semua request sudah melewati satu titik, `httpapi.Authenticator`, jadi
memasang autentikasi asli nanti cukup menukar implementasi di `NewRouter`
tanpa menyentuh handler atau service.

Kondisi sekarang:

- **Tanpa `AUTH_TOKENS`** — semua request diterima. Identitas pemain dibaca apa
  adanya dari header `X-User-Id` bila ada, dan dipakai untuk memeriksa
  kepemilikan outfit (`403 not_owner`). Header ini **tidak diverifikasi**.
- **Dengan `AUTH_TOKENS`** — `Authorization: Bearer <token>` wajib dan dicek
  terhadap daftar token layanan. Ini kunci antar-layanan (game server ke
  backend), bukan autentikasi pemain.

Yang masih harus dikerjakan: verifikasi identitas pemain sungguhan
(mis. lewat token yang ditandatangani game server), lalu hapus pembacaan
`X-User-Id` di `internal/httpapi/auth.go`.

## Konfigurasi

| Env | Bawaan | Keterangan |
| --- | --- | --- |
| `PORT` | `8080` | Port listen |
| `HOST` | `0.0.0.0` | Alamat bind |
| `APP_ENV` | `development` | Nama environment untuk log |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DATABASE_URL` | kosong | DSN Postgres; kosong = penyimpanan in-memory |
| `DB_MAX_CONNS` | `10` | Ukuran maksimum connection pool |
| `DB_MIN_CONNS` | `2` | Koneksi yang dijaga tetap terbuka |
| `DB_CONNECT_TIMEOUT` | `10s` | Batas waktu menyambung ke Postgres |
| `REDIS_URL` | kosong | Kosong = tanpa cache |
| `CACHE_TTL` | `1m` | Umur entri cache baca |
| `CACHE_PREFIX` | `avatar-catalog` | Pemisah keyspace antar environment |
| `SEED_DATA` | `true` | Data contoh untuk mode in-memory; diabaikan bila `DATABASE_URL` diisi |
| `AUTH_TOKENS` | kosong | Daftar Bearer token layanan, dipisah koma |
| `IDEMPOTENCY_TTL` | `24h` | Umur simpan hasil POST per kunci |
| `SYNC_RUN_LIMIT` | `5` | Jumlah laporan sync per jendela |
| `SYNC_RUN_WINDOW` | `1m` | Panjang jendela pembatas |
| `SHUTDOWN_TIMEOUT` | `10s` | Batas waktu shutdown yang rapi |
| `POSTGRES_DB` | `avatar_catalog` | Nama database (service `db`) |
| `POSTGRES_USER` | `avatar` | User database |
| `POSTGRES_PASSWORD` | `avatar_dev_password` | Password database, ganti di luar lokal |
| `POSTGRES_PORT` | `5432` | Port Postgres yang dipublikasikan ke host |
| `REDIS_PORT` | `6379` | Port Redis yang dipublikasikan ke host |
| `REDIS_MAXMEMORY` | `256mb` | Batas memori Redis; penuh = buang yang paling jarang dipakai |

## Struktur

```
cmd/server              # entry point, perakitan dependensi
internal/model          # 13 entitas sesuai ERD, murni data
internal/store          # port penyimpanan + implementasi in-memory + data contoh
internal/store/postgres # implementasi pgx
internal/store/cached   # pembungkus cache baca di atas port yang sama
internal/service        # aturan bisnis: outfit, katalog, transaksi
internal/httpapi        # rute, handler, DTO, middleware, seam autentikasi
internal/cache          # port cache + Redis + Noop
internal/apierr         # amplop error tunggal
internal/paging         # cursor keyset dan posisi
internal/idempotency, internal/ratelimit, internal/config
db/init                 # skema Postgres (ERD v3) + data contoh
```

Ketiga lapisan penyimpanan berbicara lewat interface yang sama di
`internal/store`, jadi `service` tidak tahu apakah datanya datang dari memori,
Postgres, atau cache:

```
service → cached.Outfits/Catalog → postgres.* → Postgres
                    ↓
                  Redis
```
