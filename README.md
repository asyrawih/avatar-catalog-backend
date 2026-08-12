# Avatar Catalog Backend

Backend katalog avatar Roblox dalam Go. Bentuk API mengikuti collection Postman
`avatar-catalog-api`, dan skemanya mengikuti ERD v3 di FigJam (13 tabel).

Akses dijaga kunci API ber-scope — lihat [Autentikasi](#autentikasi) dan
[docs/auth.md](docs/auth.md).

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

### Server dengan reverse proxy sendiri

Kalau server sudah punya Caddy milik stack lain, `docker-compose.server.yml`
membuat `api` menempel ke network proxy itu (alias `avatar-catalog-api`) dan
berhenti membuka port ke host:

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d
```

Lupa salah satu `-f` akan me-recreate `api` tanpa network proxy. Caddy lalu
gagal me-resolve `avatar-catalog-api` dan menjawab `502` untuk semua request —
gejalanya mirip proxy mati, padahal proxy-nya sehat. Supaya tidak bergantung
pada ingatan, isi `.env` **di server** dengan:

```
COMPOSE_FILE=docker-compose.yml:docker-compose.server.yml
```

Setelah itu `docker compose up -d` polos pun ikut override. Jangan pasang baris
itu di `.env` lokal: network `EDGE_NETWORK` tidak ada di mesin pengembangan,
dan override juga menutup port `8080` ke host.

### Kubernetes / k3s

Manifest lengkapnya ada di [k8s/](k8s) dan panduan deploy dari server kosong
sampai domain aktif ada di
[docs/deploy-k8s.md](docs/deploy-k8s.md).

```bash
./k8s/deploy.sh dev     # atau: ./k8s/deploy.sh prod / k3s
```

Urutan operasional lengkap untuk k3s satu node (produksi):

1. Pasang k3s, cek `kubectl get nodes` — [1.1](docs/deploy-k8s.md#11-pasang-k3s)
2. **Build image dari source terkini** dan push ke registry, lalu isi
   `images.newTag` di `k8s/overlays/prod/kustomization.yaml` — jangan pakai
   image lokal lama, lihat peringatan di
   [1.4](docs/deploy-k8s.md#14-siapkan-image)
3. Ganti host di `k8s/overlays/prod/patch-ingress.yaml`, pastikan DNS-nya
   sudah mengarah ke server
4. Pasang cert-manager, lalu apply
   [`k8s/overlays/k3s/cluster-issuer.yaml`](k8s/overlays/k3s/cluster-issuer.yaml)
   (ganti `email` ACME-nya dulu) dan tunggu `READY: True` —
   [1.6](docs/deploy-k8s.md#16-domain-dan-tls)
5. `./k8s/deploy.sh k3s` — tunggu semua pod `Running`
6. Ganti password Postgres **sebelum** pod db pertama kali start —
   [1.7](docs/deploy-k8s.md#17-secret-produksi)
7. Terbitkan kunci API dengan `cmd/apikey`; tanpa ini `/v1` selalu `401` —
   [1.8](docs/deploy-k8s.md#18-kunci-api)
8. Jalankan smoke test enam butir dari domain publik —
   [1.9](docs/deploy-k8s.md#19-smoke-test)

#### Terbitkan kunci API di k3s

`cmd/apikey` butuh akses langsung ke Postgres, sedangkan database sengaja
tidak diekspos ke luar cluster — port-forward dulu:

```bash
kubectl -n avatar-catalog port-forward svc/avatar-catalog-db 5432:5432 &
export DATABASE_URL="postgres://avatar:<password>@localhost:5432/avatar_catalog?sslmode=disable"
go run ./cmd/apikey issue --name <nama-konsumen> --role game-server --expires 90d
go run ./cmd/apikey list
```

Ganti `<password>` dengan isi `POSTGRES_PASSWORD` di Secret
`avatar-catalog-secret`, dan `<nama-konsumen>` bebas asal unik untuk
diidentifikasi lewat `apikey list`. Role yang tersedia ada di tabel
[Autentikasi](#autentikasi). Token utuh (`acb_live_...`) hanya ditampilkan
sekali saat `issue` — database cuma menyimpan hash-nya.

Rilis versi baru, migrasi skema, backup Postgres, dan debug ada di
[Bagian 2 — Operasional](docs/deploy-k8s.md#bagian-2--operasional) di panduan
yang sama.

### Skema

[db/init/001_schema.sql](db/init/001_schema.sql) berisi 13 tabel ERD v3,
[db/init/002_seed.sql](db/init/002_seed.sql) mengisi data contoh yang sama
dengan mock Postman, [db/init/003_cashback.sql](db/init/003_cashback.sql)
menambah tabel cashback (`cashback_entry`, `redeem_request`, `cashback_event`),
dan [db/init/004_engagement.sql](db/init/004_engagement.sql) menambah
`outfit_like` dan `outfit_view` — lihat [Like dan view](#like-dan-view).
Semuanya dijalankan Postgres **hanya saat volume masih kosong**, jadi setelah
mengubah skema jalankan:

```bash
docker compose down -v && docker compose up -d db
```

Beberapa aturan API sudah ditegakkan di tingkat skema, bukan hanya di kode:
`transaction.idempotency_key` unik (retry tidak bisa membuat baris ganda),
`outfit.reference_id` unik, dan primary key `(outfit_id, asset_id)` pada
`outfit_item` (satu asset tidak bisa masuk dua kali ke outfit yang sama).
Slot **tidak** unik: dua asset boleh menempati slot yang sama, karena slot cuma
label yang dilaporkan klien dan kliennya yang tahu apakah keduanya bentrok.

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
setiap penulisan outfit (create/update/delete) menaikkan satu penghitung
global (`ver:outfit:all`) sehingga **seluruh** cache outfit — detail maupun
daftar semua pemain — tidak terbaca lagi dan hilang sendiri saat TTL habis.
Menghapus per pola berarti memindai seluruh keyspace Redis, yang justru paling
berat persis ketika data paling sering berubah. Kunci idempotensi di Redis
yang sama tidak tersentuh.

Konsekuensinya:

- `POST /v1/catalog/sync-runs` yang menandai asset mati langsung menggugurkan
  seluruh cache katalog — item mati tidak akan bertahan di etalase sampai TTL
  habis.
- Perubahan outfit pemain mana pun menggugurkan seluruh cache outfit sekaligus.
- `POST /v1/outfits/resolve` sengaja tidak di-cache: kombinasi `referenceId`
  dari feed hampir selalu berbeda, jadi entrinya nyaris tak pernah terpakai ulang.
- Like dan view **tidak** menggugurkan cache. Invalidasinya global, sedangkan
  view tercatat pada hampir setiap outfit yang dibuka — kalau digugurkan, cache
  praktis selalu kosong dan lapisan ini berubah jadi beban murni. Harganya:
  `likeCount`/`viewCount` di daftar dan urutan `mostLiked`/`mostViewed` bisa
  tertinggal paling lama `CACHE_TTL`. Balasan atas aksi like/view itu sendiri
  tetap akurat — angkanya dibaca di dalam transaksi penulisan, bukan dari
  pembacaan ter-cache.

Kegagalan Redis tidak pernah menjatuhkan permintaan — semua error cache
diperlakukan sebagai miss dan dilayani langsung dari database.

Kunci idempotensi untuk `POST /v1/outfits` dan `POST /v1/catalog/sync-runs`
juga disimpan di Redis (TTL `IDEMPOTENCY_TTL`). Idempotensi transaksi tetap di
Postgres lewat kolom unik, karena itu batasan domain yang harus bertahan walau
Redis dikosongkan.

## Endpoint

Postman collection lengkapnya ada di [postman/](postman) — 34 request, sudah
diuji lewat `newman` terhadap server sungguhan (34/34 request, 43/43 assertion
lulus). Variabel `outfitId`/`txId`/`cursor` terisi otomatis antar request.

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/v1/outfits?userId=&isPublic=&q=&outfitId=&sort=&limit=&cursor=` | Daftar outfit beserta itemnya; `sort=recent` (bawaan) / `mostLiked` / `mostViewed`, semua penyaring opsional |
| POST | `/v1/outfits` | Buat outfit beserta `body` avatar; backend membangkitkan `referenceId` |
| GET | `/v1/outfits/{outfitId}` | Detail outfit beserta item |
| PATCH | `/v1/outfits/{outfitId}` | Ubah sebagian metadata, termasuk simpan `recoItemId` |
| PUT | `/v1/outfits/{outfitId}/items` | Ganti seluruh isi item |
| DELETE | `/v1/outfits/{outfitId}` | Soft delete (isi `deletedAt`) |
| POST | `/v1/outfits/{outfitId}/likes` | Sukai outfit (butuh identitas pemain); idempoten |
| DELETE | `/v1/outfits/{outfitId}/likes` | Batalkan like; idempoten |
| POST | `/v1/outfits/{outfitId}/views` | Catat satu kali dilihat; boleh anonim |
| POST | `/v1/outfits/resolve?limit=&cursor=` | Tukar sekumpulan `referenceId` jadi metadata render, berhalaman |
| GET | `/v1/templates?limit=&cursor=` | Daftar rig terdaftar, terbaru dulu |
| POST | `/v1/templates` | Daftarkan rig beserta nama dan gender |
| GET | `/v1/templates/{templateId}` | Detail satu rig |
| PATCH | `/v1/templates/{templateId}` | Isi nama dan gender rig |
| GET | `/v1/catalog/sections/{sectionId}/items` | Isi etalase, hanya item `active` |
| POST | `/v1/catalog/items:batch-get` | Ambil banyak item katalog sekaligus |
| POST | `/v1/catalog/sync-runs` | Laporan sync worker |
| POST | `/v1/transactions` | Catat transaksi (wajib `Idempotency-Key`); cashback ter-accrue otomatis |
| GET | `/v1/transactions?userId=&limit=&cursor=` | Riwayat transaksi pemain |
| GET | `/v1/cashback/summary?userId=` | Saldo, rate berjalan + progres bonus, request pending |
| GET | `/v1/cashback/entries?userId=&limit=&cursor=` | Ledger cashback pemain, terbaru dulu |
| POST | `/v1/cashback/redeems` | Buat request redeem seluruh saldo (min 100, satu pending per pemain) |
| GET | `/v1/cashback/redeems?userId=&status=&limit=&cursor=` | Daftar request; tanpa `userId` = antrean internal |
| PATCH | `/v1/cashback/redeems/{requestId}` | Tuntaskan request: `completed` / `rejected` (mengembalikan saldo) |
| POST | `/v1/cashback/reversals` | Tarik kembali cashback transaksi yang di-refund (`{"txId": ...}`) |
| GET / POST | `/v1/cashback/events` | Lihat / jadwalkan jendela bonus event |
| GET | `/v1/cashback/reconciliation?from=&to=` | Agregat ledger (Robux) untuk rekonsiliasi periodik |
| GET | `/healthz`, `/readyz` | Probe kesehatan |

## Like dan view

`likeCount` dan `viewCount` ikut di setiap baris daftar dan detail outfit, dan
`GET /v1/outfits?sort=mostLiked|mostViewed` mengurutkan berdasarkan keduanya.

```bash
curl -X POST  localhost:8080/v1/outfits/otf_9f2a41/likes -H 'X-User-Id: 627278822'
curl -X DELETE localhost:8080/v1/outfits/otf_9f2a41/likes -H 'X-User-Id: 627278822'
curl -X POST  localhost:8080/v1/outfits/otf_9f2a41/views
curl "localhost:8080/v1/outfits?sort=mostLiked&limit=20"
```

Datanya disimpan **dua lapis**, dan itu disengaja:

- `outfit_like` / `outfit_view` — log kejadian per pemain. Ini bahan mentah
  untuk melatih generator outfit: model butuh tahu *siapa* menyukai *apa*.
  Begitu diringkas jadi angka, sinyal itu hilang dan tidak bisa direkonstruksi.
- `outfit.like_count` / `view_count` — ringkasan yang ikut terbawa di tiap baris
  daftar. Tanpa ini setiap `GET /v1/outfits` harus meng-`COUNT` dua tabel per
  outfit.

Counter diperbarui di transaksi yang sama dengan penulisan log. Yang mengikat
tetap tabel log; kalau counter dicurigai melenceng, query rekonsiliasinya ada di
komentar bawah [db/init/004_engagement.sql](db/init/004_engagement.sql).

Aturan yang membedakan keduanya:

- **Like idempoten dan butuh identitas.** Satu pemain satu like per outfit,
  ditegakkan primary key `(outfit_id, user_id)` — bukan pemeriksaan "sudah suka
  belum?" di aplikasi, yang bisa kalah balapan dan menaikkan counter dua kali.
  Like berulang menjawab `200` dengan `changed:false`, bukan `409`. Tanpa
  identitas pemain: `401`.
- **View append-only dan boleh anonim.** Pemain yang membuka outfit yang sama
  lima kali memang lima sinyal — berapa kali dilihat sebelum disukai adalah
  bagian dari datanya. Konsekuensinya `outfit_view` tumbuh paling cepat di
  seluruh skema; kalau nanti terlalu besar, ringkas per hari ke tabel agregat
  lebih dulu, jangan langsung dibuang.
- **View dicatat lewat endpoint sendiri, bukan efek samping `GET`.** `GET`
  tetap murni baca sehingga aman di-cache dan diulang, dan klien yang menentukan
  kapan sebuah outfit benar-benar terlihat — bukan setiap crawler yang kebetulan
  mengambilnya.
- **`likedByMe`** hanya muncul untuk pemanggil yang dikenali. Permintaan anonim
  tidak membawanya sama sekali, bukan membawanya bernilai `false`: "tidak tahu"
  beda dari "tidak suka".

Paginasi urutan populer memakai keyset `(count, outfitId)`, bukan
`(updatedAt, outfitId)` seperti urutan bawaan. Cursor menyimpan sort asalnya,
jadi cursor `mostLiked` yang dipakai di `sort=recent` ditolak `400`
(`cursor_sort_mismatch`) alih-alih diam-diam mengembalikan halaman ngawur.

## Cashback

Cashback dihitung **di backend, bukan di game** — game hanya menampilkan angka
dari `GET /v1/cashback/summary`. Semua nilai dalam Robux; tidak ada mata uang
lain di mana pun dalam sistem ini.

Rate efektif = `min(20% + bonus aktif, 40%)`, cashback per transaksi =
`floor(spend × rate)` dengan `spend` = jumlah harga item `result='success'`
pada transaksi itu. Jalur bonus:

| Bonus | Nilai | Syarat (dihitung dari `transaction` + `transaction_item`) |
| --- | --- | --- |
| `event` | +10% | Ada jendela `cashback_event` yang mencakup `occurredAt` |
| `streak` | +5% | Ada spend sukses pada 2 hari (UTC) tepat sebelum hari transaksi |
| `first` | +10% | Belum pernah ada transaksi dengan spend sukses sebelumnya |
| `loyal` | +5% | Total spend sukses kumulatif sebelumnya ≥ 5.000 R$ |

Hasil hitung disimpan di ledger **append-only** `cashback_entry` (saldo =
`SUM(amount)`), supaya rate yang berlaku saat transaksi terjadi tidak berubah
saat jadwal event bergeser. Accrual menempel pada `POST /v1/transactions` dan
idempoten lewat batasan unik `(tx_id, kind)` — retry tidak menggandakan
cashback, dan jalur replay ikut menuntaskan accrual yang sempat gagal.

Redeem memotong **seluruh saldo** saat request dibuat (minimum 100 R$, maksimal
satu `pending` per pemain — ditegakkan indeks unik parsial). Fulfillment
dikerjakan tim internal di luar sistem ini; hasilnya masuk lewat
`PATCH /v1/cashback/redeems/{requestId}` (`rejected` mengembalikan potongan).
Refund/chargeback ditarik lewat `POST /v1/cashback/reversals`; saldo boleh
minus — itu disengaja.

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

**Pencarian di daftar outfit.** `q` mencocokkan sebagian nama outfit tanpa
peduli huruf besar-kecil (`q=streetwear` menemukan "Y2K Streetwear"); `%` dan
`_` di dalamnya dicari apa adanya, bukan sebagai wildcard. `outfitId` mengambil
beberapa outfit sekaligus dan boleh diulang maupun dipisah koma:

```bash
curl "http://localhost:8080/v1/outfits?q=streetwear&isPublic=true"
curl "http://localhost:8080/v1/outfits?outfitId=otf_9f2a41,otf_3c88de"
```

Keduanya bisa dipadukan dengan `userId`, `isPublic`, dan penomoran halaman.
Batasnya: `q` maksimal 120 karakter (`invalid_keyword`) dan `outfitId` maksimal
100 id per permintaan (`too_many_outfit_ids`). `outfitId` yang tidak ada
menghasilkan daftar kosong, bukan 404 — beda dengan `GET /v1/outfits/{outfitId}`.

**Body avatar.** Item saja tidak cukup untuk merender ulang avatar — dua outfit
dengan item identik tetap terlihat berbeda kalau warna kulit dan skala tubuhnya
beda. `POST /v1/outfits` menerima keduanya lewat field `body`, dan setiap
respons outfit mengembalikannya dalam bentuk yang sama:

```json
{
  "body": {
    "colors": { "head": "AE7C64", "torso": "AE7C64", "leftArm": "AE7C64",
                "rightArm": "AE7C64", "leftLeg": "AE7C64", "rightLeg": "AE7C64" },
    "scales": { "height": 1.0499999523162842, "width": 1, "head": 1,
                "depth": 1, "bodyType": 1, "proportion": 0 }
  }
}
```

`body` opsional, begitu juga `colors` dan `scales` di dalamnya — outfit yang
tidak melaporkannya dijawab `"body": null`. Warna adalah hex RGB 6 digit;
`#` di depan diterima dan dilepas, besar-kecil huruf dibiarkan seperti yang
dikirim. `height`, `width`, `head`, dan `depth` adalah pengali dan harus lebih
besar dari nol; `bodyType` dan `proportion` adalah bobot yang boleh nol.
Rentang atasnya tidak dibatasi — batas Roblox bergeser antar rig dan antar
rilis, jadi menebaknya di sini hanya akan menolak avatar yang sah.

Disimpan sebagai satu kolom `jsonb` di `OUTFIT.body`, bukan dua belas kolom:
isinya blob render yang tidak pernah dipakai menyaring maupun mengurutkan.

Belum ada di `PATCH /v1/outfits/{outfitId}` — body hanya bisa diisi saat outfit
dibuat.

**Daftar membawa item.** Ringkasan outfit di `GET /v1/outfits` dan
`POST /v1/outfits/resolve` menyertakan `items` dan `body` selengkap `GET`
detail, jadi klien tidak perlu menyusul satu permintaan per outfit hanya untuk
tahu isinya.
Ini gratis di sisi database: penyimpanan memang sudah mengambil item satu
halaman penuh dalam satu query. Yang bertambah cuma ukuran respons — dengan
`limit=100` dan outfit terisi penuh, satu halaman bisa mencapai ratusan
kilobyte. Kecilkan `limit` kalau itu terasa. Field `itemCount` tetap ada.

**Resolve berhalaman.** Feed rekomendasi bisa membawa ratusan `referenceId`
sekaligus, sementara tiap outfit di respons membawa seluruh item dan body-nya.
Karena itu `POST /v1/outfits/resolve` ikut berhalaman, dengan `limit` dan
`cursor` di query string dan envelope yang sama dengan `GET /v1/outfits`:

```bash
curl -X POST "http://localhost:8080/v1/outfits/resolve?limit=50" \
  -H 'Content-Type: application/json' \
  -d '{"referenceIds":["550e8400-...","6ba7b810-..."]}'
```

Body permintaan tetap murni daftar id: klien mengirim **seluruh** daftar yang
sama di tiap halaman dan hanya menukar `cursor` di URL. Cursor di sini menandai
posisi di dalam daftar itu, jadi hanya sepotongnya yang benar-benar diambil
dari penyimpanan. Batasnya 500 `referenceId` per permintaan
(`413 too_many_ids`), jauh di atas satu halaman karena yang dikirim adalah
seluruh feed sedangkan yang diambil hanya sepotong.

Respons juga membawa `total` (jumlah `referenceId` yang dikirim) dan
`totalPages`. Keduanya pasti dan gratis: yang dihitung adalah daftar milik
klien, bukan isi database, jadi tidak ada query hitung tambahan. `GET
/v1/outfits` sengaja tidak punya keduanya — di sana totalnya baru diketahui
lewat `COUNT` ke tabel outfit di tiap permintaan.

Perhatikan `notFound`: isinya hanya `referenceId` dari halaman yang sedang
diminta. Id di halaman berikutnya belum pernah dicari, jadi belum bisa disebut
tidak ditemukan — kumpulkan `notFound` dari semua halaman kalau butuh totalnya.

**Cursor, bukan offset.** Katalog berubah terus karena sync worker, jadi offset
akan menggeser hasil di antara dua permintaan. Cursor dikodekan base64url dan
buram bagi klien: daftar outfit dan transaksi memakai keyset `(waktu, id)`, isi
etalase memakai posisi karena urutannya stabil. Daftar outfit yang diurutkan
populer (`sort=mostLiked|mostViewed`) memakai keyset `(pencacah, outfitId)` —
kunci yang berbeda, jadi cursor menyimpan sort asalnya dan menolak `400`
(`cursor_sort_mismatch`) kalau dipakai pada urutan lain. Resolve juga memakai posisi —
yang dinomori bukan isi database, melainkan daftar `referenceId` yang klien
sendiri kirim ulang di tiap halaman, jadi urutannya stabil menurut definisi.

**Like disimpan sebagai kejadian, bukan cuma angka.** `outfit_like` dan
`outfit_view` menyimpan pasangan (pemain, outfit) beserta waktunya, sedangkan
`like_count`/`view_count` di `outfit` hanya ringkasan untuk baca cepat. Counter
saja sudah cukup untuk mengurutkan populer, tapi tidak untuk melatih generator
outfit — di situ justru *siapa menyukai apa* yang jadi sinyalnya, dan sekali
data diringkas jadi angka, sinyal itu tidak bisa direkonstruksi. Rinciannya di
[Like dan view](#like-dan-view).

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

## Autentikasi

Setiap request ke `/v1` wajib membawa kunci API:

```
Authorization: Bearer acb_live_cfwnlwemjelnw_q3tnhfmsofczncrx5kotd7bxyf6ir75...
```

Probe kesehatan (`/healthz`, `/readyz`) sengaja tidak butuh kunci —
orchestrator tidak memegang kredensial, dan probe yang butuh kunci akan
menandai pod sehat sebagai tidak sehat begitu kuncinya dirotasi.

Kunci hidup di tabel `api_key` sebagai **hash**, diterbitkan lewat CLI:

```bash
export DATABASE_URL=postgres://...
go run ./cmd/apikey issue --name roblox-game-server-prod --role game-server --expires 90d
go run ./cmd/apikey list
go run ./cmd/apikey revoke <keyId>
```

Token utuh hanya ditampilkan sekali. Dump database, backup, atau kebocoran log
tidak memberi satu pun token yang bisa dipakai. Rotasi dan pencabutan berlaku
seketika tanpa redeploy.

| Role | Untuk | Tidak bisa |
|---|---|---|
| `game-server` | Roblox server-side | menuntaskan redeem (`cashback:admin`) |
| `dashboard` | Tool internal tim | bertindak atas nama pemain (`actor:assert`) |
| `ai` | Pengambil data latih | menulis apa pun |
| `public-read` | Calon API publik | segalanya kecuali baca katalog |

Dua pembagian yang paling penting:

- **Game server tidak punya `cashback:admin`.** Menuntaskan redeem dan menarik
  cashback adalah jalur uang keluar; kunci game server yang bocor tidak bisa
  mencairkan apa pun.
- **Hanya game server punya `actor:assert`**, yaitu izin berkata "saya
  bertindak atas nama pemain X" lewat header `X-User-Id`. Kunci dashboard atau
  AI yang bocor tetap tidak bisa menyukai atau menukar cashback atas nama siapa
  pun.

Panduan lengkapnya — pemasangan sisi Roblox, rotasi, dan alasan tiap keputusan
desain — ada di [docs/auth.md](docs/auth.md). Modul Luau-nya di
[roblox/AvatarCatalog.lua](roblox/AvatarCatalog.lua).

Catatan: audit memori beserta perbaikannya ada di
[docs/audit-memori.md](docs/audit-memori.md).

`AUTH_REQUIRED=false` mematikan seluruh pemeriksaan dan mempercayai `X-User-Id`
apa adanya. Hanya untuk pengembangan lokal; ditolak saat `APP_ENV=production`.

### Batas request

`RATE_LIMIT_PER_SECOND` (bawaan `50`) membatasi request **per kunci API**, bukan
per alamat IP. Semua request dari satu game server datang dari IP yang sama,
jadi membatasi per IP berarti membatasi seluruh pemain di server itu sebagai
satu kesatuan — dan sebaliknya, penyerang di belakang banyak IP tidak terbatasi
sama sekali. `keyId` juga yang kita cabut kalau ada konsumen yang mengamuk,
jadi masuk akal batasnya melekat di situ.

Yang melewati batas dijawab `429 rate_limited` beserta header `Retry-After`.
Probe kesehatan tidak ikut dibatasi; kalau ikut, pod sehat akan menandai
dirinya sendiri tidak sehat.

**Batasnya per proses, bukan per cluster.** Tiap replika punya hitungannya
sendiri, jadi batas efektif satu kunci adalah nilai ini dikali jumlah replika —
dengan 3 replika dan nilai `50`, satu kunci sebenarnya bisa 150 request/detik.
Bagi dulu kalau yang kamu maksud batas gabungan. Hitungan bersama lintas
replika butuh Redis dan belum dikerjakan.

**Yang belum dikerjakan:** verifikasi identitas pemain sungguhan. Model
sekarang "penerbit tepercaya": backend percaya `X-User-Id` dari kunci
ber-`actor:assert`, tanpa memverifikasinya ke Roblox. Cukup selama hanya game
server yang memegang kunci itu.

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
| `AUTH_REQUIRED` | `true` | Wajibkan kunci API di semua rute `/v1`; `false` ditolak saat `APP_ENV=production` |
| `RATE_LIMIT_PER_SECOND` | `50` | Batas request per detik per kunci API; `0` = mati. Dihitung per proses, bukan per cluster |
| `IDEMPOTENCY_TTL` | `24h` | Umur simpan hasil POST per kunci |
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
cmd/apikey              # terbitkan, lihat, dan cabut kunci API
internal/model          # 13 entitas sesuai ERD, murni data
internal/store          # port penyimpanan + implementasi in-memory + data contoh
internal/store/postgres # implementasi pgx
internal/store/cached   # pembungkus cache baca di atas port yang sama
internal/service        # aturan bisnis: outfit, katalog, transaksi
internal/httpapi        # rute, handler, DTO, middleware, seam autentikasi
internal/cache          # port cache + Redis + Noop
internal/auth           # format kunci API, scope, dan role
internal/apierr         # amplop error tunggal
internal/paging         # cursor keyset dan posisi
internal/idempotency, internal/ratelimit, internal/config
db/init                 # skema Postgres (ERD v3), cashback, like/view, kunci API + data contoh
roblox                  # modul Luau untuk game server
```

Ketiga lapisan penyimpanan berbicara lewat interface yang sama di
`internal/store`, jadi `service` tidak tahu apakah datanya datang dari memori,
Postgres, atau cache:

```
service → cached.Outfits/Catalog → postgres.* → Postgres
                    ↓
                  Redis
```
