# Autentikasi: kunci API

Setiap request ke `/v1` wajib membawa kunci API:

```
Authorization: Bearer acb_live_cfwnlwemjelnw_q3tnhfmsofczncrx5kotd7bxyf6ir75...
```

Probe kesehatan (`/healthz`, `/readyz`) sengaja **tidak** butuh kunci —
orchestrator tidak memegang kredensial, dan probe yang butuh kunci akan
menandai pod sehat sebagai tidak sehat begitu kuncinya dirotasi.

## Menerbitkan kunci

```bash
export DATABASE_URL=postgres://...

go run ./cmd/apikey issue --name roblox-game-server-prod --role game-server --expires 90d
go run ./cmd/apikey issue --name dashboard-internal     --role dashboard   --expires 365d
go run ./cmd/apikey issue --name ai-trainer             --role ai          --expires 90d

go run ./cmd/apikey list
go run ./cmd/apikey revoke <keyId>
go run ./cmd/apikey roles
```

**Token utuh hanya ditampilkan sekali.** Yang masuk database cuma SHA-256-nya,
jadi token yang hilang tidak bisa dipulihkan — hanya bisa diterbitkan ulang.
Itu memang yang diinginkan: dump database, backup, atau kebocoran log tidak
memberi penyerang satu pun token yang bisa dipakai.

## Role dan scope

| Role | Scope | Untuk |
|---|---|---|
| `game-server` | `catalog:read` `catalog:write` `transactions:write` `transactions:read` `cashback:read` `cashback:redeem` `actor:assert` | Roblox server-side |
| `dashboard` | `catalog:read` `transactions:read` `cashback:read` `cashback:admin` | Tool internal tim |
| `ai` | `catalog:read` `transactions:read` | Pengambil data latih |
| `public-read` | `catalog:read` | Calon API publik |

Dua pembagian yang paling penting, dan keduanya dijaga test:

- **Game server tidak punya `cashback:admin`.** Menuntaskan redeem dan menarik
  cashback adalah jalur uang keluar. Kunci game server yang bocor tidak bisa
  mencairkan apa pun — paling jauh membuat *request* redeem, yang masih harus
  disetujui pemegang kunci dashboard.
- **Hanya game server yang punya `actor:assert`.** Scope ini yang mengizinkan
  sebuah kunci berkata "saya sedang bertindak atas nama pemain 627278822" lewat
  header `X-User-Id`. Kunci dashboard atau AI yang bocor tetap tidak bisa
  menyukai, membuat outfit, atau menukar cashback atas nama siapa pun.

Scope juga bisa dipilih satu per satu kalau role bawaan tidak pas:

```bash
go run ./cmd/apikey issue --name eksperimen --scopes catalog:read,transactions:read
```

Scope ditegakkan **per rute di router**, bukan diperiksa di dalam handler: rute
baru yang lupa diberi scope langsung terlihat saat membaca
`internal/httpapi/router.go`, sedangkan pemeriksaan yang lupa ditulis di dalam
handler tidak kelihatan dari mana pun.

## Bertindak atas nama pemain

Panggilan yang mewakili seorang pemain membawa dua hal sekaligus:

```
Authorization: Bearer acb_live_...      <- kunci milik game server
X-User-Id: 627278822                    <- pemain yang sedang diwakili
```

Backend memeriksa keduanya: kuncinya harus sah, DAN kunci itu harus punya
`actor:assert`. Tanpa scope itu, header `X-User-Id` dijawab `403
actor_assert_forbidden` — bukan diam-diam diabaikan, karena permintaan yang
mengira dirinya berjalan atas nama pemain lalu berjalan sebagai anonim adalah
sumber bug yang sulit dilacak.

Modelnya "penerbit tepercaya": game server sudah membuktikan dirinya lewat
kunci, dan hanya dia yang tahu pemain mana yang sedang bermain. Backend tidak
memverifikasi ulang identitas pemain ke Roblox.

**Batas model ini:** siapa pun yang memegang kunci game server bisa mengaku
sebagai pemain mana pun. Itu sebabnya kunci itu tidak boleh pernah sampai ke
klien Roblox — lihat bagian berikut.

## Sisi Roblox

Modulnya ada di [`roblox/AvatarCatalog.lua`](../roblox/AvatarCatalog.lua).

1. Terbitkan kunci dengan `--role game-server`.
2. Simpan tokennya sebagai **Secret** di Creator Dashboard → Experience →
   Secrets:
   - nama: `AVATAR_CATALOG_KEY`
   - domain: host backend, mis. `api.contoh.com` (tanpa `https://`)
3. Aktifkan HTTP request di Game Settings → Security.
4. Taruh modulnya di `ServerScriptService`.

```lua
local AvatarCatalog = require(script.Parent.AvatarCatalog)
local api = AvatarCatalog.new("https://api.contoh.com")

local result = api:likeOutfit(player.UserId, "otf_9f2a41")
if not result.ok then
    warn("gagal menyukai:", result.errorCode, result.errorMessage)
end
```

Dua aturan yang tidak boleh dilanggar:

- **Jangan pernah dari LocalScript.** Klien Roblox berjalan di mesin pemain;
  apa pun yang sampai ke sana bisa dibaca exploiter. Modulnya menolak dimuat di
  luar server (`RunService:IsServer()`), tapi jangan bergantung pada itu — kalau
  butuh dari klien, lewatkan RemoteEvent ke server dan biarkan server yang
  memanggil backend.
- **Simpan sebagai Secret, bukan string di dalam Script.** Nilai Secret tidak
  pernah menjadi string biasa di Luau (`Secret:AddPrefix("Bearer ")` tetap
  mengembalikan Secret), jadi tidak bisa bocor lewat source, `print`, maupun
  pesan error. Token yang ditulis langsung di Script ikut ke dalam file `.rbxl`
  dan riwayatnya.

## Rotasi dan pencabutan

Kunci hidup di tabel `api_key`, jadi keduanya berlaku **seketika tanpa
redeploy** — pada request berikutnya, bukan setelah restart.

Rotasi tanpa downtime:

```bash
# 1. Terbitkan kunci baru dengan role yang sama
go run ./cmd/apikey issue --name roblox-game-server-prod-v2 --role game-server --expires 90d

# 2. Pasang tokennya di Secret Roblox (menimpa yang lama)

# 3. Pastikan yang lama sudah tidak dipakai
go run ./cmd/apikey list      # lihat kolom DIPAKAI TERAKHIR

# 4. Cabut yang lama
go run ./cmd/apikey revoke <keyId lama>
```

Kalau ada kunci yang bocor, langsung `revoke` — jangan tunggu penggantinya
siap. Konsumen yang memakainya akan mendapat `401` sampai dipasangi kunci baru,
dan itu jauh lebih murah daripada membiarkannya hidup.

Kolom `last_used_at` diperbarui paling sering sekali per menit per kunci, bukan
tiap request: tanpa ambang itu, satu game server yang sibuk berarti satu
`UPDATE` per panggilan API, semuanya berebut baris yang sama.

## Keputusan desain

**Bentuk token `acb_<env>_<keyId>_<secret>`.**
`acb` memudahkan pemindai rahasia mengenalinya. `env` (`live`/`test`) membuat
kunci produksi tidak bisa tertukar dengan kunci lokal. `keyId` publik dan ada
supaya verifikasi cukup satu lookup indeks — tanpa itu backend harus membaca
seluruh kunci lalu membandingkan satu per satu, makin lambat seiring bertambah
konsumen, dan lama pencariannya ikut membocorkan informasi.

**Base32, bukan base64url.** Alfabet base64url memuat `_`, padahal `_` dipakai
sebagai pemisah bagian — token yang rahasianya kebetulan memuat `_` akan
terbaca sebagai lima bagian dan ditolak walau sah.

**SHA-256, bukan bcrypt/argon2.** Keduanya dirancang untuk rahasia berentropi
rendah seperti kata sandi manusia, yang perlu diperlambat supaya tidak bisa
ditebak dari kamus. Token di sini 256 bit acak — tidak ada kamus yang bisa
menebaknya, jadi memperlambat verifikasi hanya menambah latensi di setiap
request tanpa menambah keamanan sedikit pun.

**Satu balasan untuk semua kegagalan.** Bentuk token salah, `keyId` tidak ada,
hash tidak cocok, kunci dicabut, kunci kedaluwarsa — semuanya dijawab `401
unauthenticated` dengan pesan yang sama. Membedakannya akan memberi tahu
penyerang seberapa jauh tebakannya sudah benar; "kunci dicabut" bahkan
mengonfirmasi bahwa tokennya pernah sah. Alasan sebenarnya tetap dicatat di log
server beserta nama kuncinya.

**Hash dicocokkan sebelum status diperiksa.** Memeriksa dicabut/kedaluwarsa
lebih dulu membuat lama balasan berbeda antara `keyId` yang ada dan yang tidak,
sehingga `keyId` yang sah bisa dipetakan tanpa pernah menebak rahasianya.
Perbandingan hash-nya sendiri constant-time.

**Gagal tertutup.** `AUTH_REQUIRED` bawaannya `true`. Server yang lupa
dikonfigurasi harus menolak semua request, bukan menerima semua request.
`AUTH_REQUIRED=false` ditolak saat `APP_ENV=production`.

## Batas request

`RATE_LIMIT_PER_SECOND` (bawaan `50`) membatasi request per kunci API. Yang
melewatinya dijawab `429 rate_limited` beserta `Retry-After`.

Kuncinya `keyId`, bukan alamat IP. Semua request dari satu game server datang
dari IP yang sama, jadi membatasi per IP berarti membatasi seluruh pemain di
server itu sebagai satu kesatuan — dan penyerang di belakang banyak IP tidak
terbatasi sama sekali. `keyId` juga yang dicabut kalau ada konsumen yang
mengamuk, jadi masuk akal batasnya melekat di situ.

Pembatas dipasang **sesudah** autentikasi. Request tanpa kunci yang sah sudah
ditolak `401` sebelum menyentuh handler mana pun, dan membatasi jalur yang
sudah tertutup hanya menambah biaya. Perlindungan terhadap banjir request tanpa
kunci adalah tugas ingress/WAF di depannya.

Probe kesehatan tidak ikut dibatasi — orchestrator memanggilnya terus-menerus,
dan kalau ikut terbatasi pod sehat akan menandai dirinya sendiri tidak sehat.

Dua batasnya yang perlu diketahui:

- **Per proses, bukan per cluster.** Tiap replika menghitung sendiri, jadi batas
  efektif satu kunci = nilai konfigurasi × jumlah replika. Hitungan bersama
  lintas replika butuh Redis; belum dikerjakan.
- **Jendela tetap, bukan sliding window.** Satu kunci bisa menghabiskan seluruh
  jatah di ujung jendela lalu seluruh jatah berikutnya di awal jendela
  setelahnya — sesaat menjadi dua kali lipat batas. Jendelanya satu detik, jadi
  efek itu paling lama berlangsung satu detik.

## Yang belum dikerjakan

- **Batas request bersama lintas replika** (lihat di atas).
- **Verifikasi identitas pemain sungguhan.** Model "penerbit tepercaya" cukup
  selama hanya game server yang memegang kunci. Kalau nanti ada konsumen yang
  bertindak atas nama pemain tanpa lewat game server, identitas pemain harus
  dibuktikan sendiri (mis. token bertanda tangan dari game server), bukan
  sekadar header.
- **Audit log penulisan.** Yang tercatat sekarang baru `last_used_at` per kunci
  dan nama kunci di log akses. Untuk API publik, penulisan sebaiknya punya
  jejaknya sendiri.
