# Audit memori — 12 Agustus 2026

Audit kebocoran memori dan overhead alokasi di seluruh basis kode, dijalankan
oleh tiga penelaah paralel dengan fokus berbeda, lalu tiap temuan diverifikasi
ulang dengan membaca kodenya sendiri sebelum diperbaiki.

**Hasil ringkas:** satu kebocoran nyata di jalur produksi (temuan 1), satu
pengali kebocoran itu (temuan 2), satu perbaikan alokasi yang terukur −75%
(temuan 3), dan satu bug operasional yang membuat shutdown normal terlihat
seperti crash (temuan 5). Enam temuan lain diperiksa lalu sengaja tidak diubah;
alasannya ditulis di bawah.

## Cakupan

| Penelaah | Fokus | Temuan |
|---|---|---|
| 1 | Pertumbuhan tak terbatas: map/slice yang tak pernah dibuang, goroutine bocor, resource tak ditutup, cache tanpa TTL | 5 |
| 2 | Overhead alokasi per request pada jalur panas (`GET /v1/outfits`, `POST /v1/transactions`) | 6 |
| 3 | Daur hidup goroutine, shutdown, konfigurasi pool, mutex, timeout | 6 |

Ketiganya baca-saja; semua perubahan di bawah ditulis dan diuji terpisah.
Temuan 1 ditemukan penelaah 1 dan 3 secara terpisah — itu menaikkan keyakinan.

---

## Yang diperbaiki

### 1. TINGGI — Rekaman idempotensi tidak pernah dibuang

`internal/idempotency/idempotency.go`

`MemoryStore` punya TTL, tapi kedaluwarsa **hanya** diperiksa di `Get` untuk
kunci yang sama persis. Kunci idempotensi unik per request: ditulis sekali,
lalu tidak pernah dibaca lagi — retry justru kejadian langka. Jadi cabang
penghapusan itu praktis tidak pernah berjalan, dan **setiap POST menambah satu
rekaman berisi body respons penuh, selamanya.** Tidak ada plateau; prosesnya
akan OOM, bukan stabil.

Ini jalur produksi, bukan hanya dev: `cmd/server/main.go` memasang
`MemoryStore` tanpa syarat dan hanya menggantinya dengan `RedisStore` bila
`REDIS_URL` diisi. Deploy Postgres tanpa Redis adalah kombinasi sah yang tidak
ditolak saat start. (Manifest k8s di repo ini selalu mengisi `REDIS_URL`, jadi
deploy k3s yang dijelaskan di `deploy-k8s.md` tidak terkena — tapi kodenya
sendiri tidak memaksakan apa pun soal itu.)

**Perbaikan:** penyapuan berkala di `Put`, meniru pola `pruneLocked` yang sudah
ada di `internal/ratelimit`. Diredam satu menit supaya menyapu seluruh map
(O(n)) tidak berubah jadi biaya kuadratik pada laju POST tinggi. Ditambah
`Len()` untuk pemantauan.

**Tes:** `internal/idempotency/sweep_test.go` — 1.000 kunci sekali pakai yang
tidak pernah dibaca ulang harus tersisa 1 setelah TTL lewat. Tanpa perbaikan,
tes ini gagal dengan 1.001.

### 2. SEDANG — Body respons direkam tanpa batas ukuran

`internal/httpapi/idempotency.go`

`captureWriter` menyangga **seluruh** body respons untuk disimpan sebagai hasil
idempoten. `maxBodyBytes` (1 MiB) hanya membatasi body **masuk**, bukan keluar.
Endpoint yang tercakup sekarang membalas satu objek outfit — kecil — jadi ini
belum menyakitkan sendirian. Masalahnya ia adalah pengali temuan 1, dan menjadi
berbahaya begitu middleware `idempotent` dipasang pada endpoint yang membalas
satu halaman penuh.

**Perbaikan:** batas 64 KiB. Yang melewatinya berhenti direkam, buffer-nya
dilepaskan, dan rekamannya tidak disimpan sama sekali — menyimpan potongan
setengah jadi lebih buruk daripada tidak menyimpan, karena replay-nya akan
mengirim JSON terpotong yang gagal di-parse klien. Klien tetap menerima respons
utuh; pembatasan ini soal apa yang disimpan, bukan apa yang dikirim.

**Tes:** `internal/httpapi/idempotency_capture_test.go` — 4 kasus, termasuk
penulisan bertahap yang totalnya melewati batas.

### 3. TINGGI (alokasi) — Pengelompokan item tanpa kapasitas awal

`internal/store/postgres/outfits.go`

`collectOutfitItems` membangun `map[string][]model.OutfitItem` tanpa size hint,
dan tiap slice item tumbuh dari nol (1→2→4→8→16→32). Ini jalur terpanas di
seluruh API: satu halaman bisa 100 outfit × sampai 30 item. Jumlah outfit sudah
diketahui pemanggil, dan query-nya sudah `ORDER BY outfit_id`, jadi kapasitasnya
gratis.

**Perbaikan:** `collectOutfitItems(rows, len(outfitIDs))` + kapasitas awal 8 per
slice. Sekalian `collectOutfits` dan `collectTransactions` diberi kapasitas dari
batas atas yang sudah dipegang pemanggil (`limit+1`, `limit`,
`len(referenceIDs)`).

**Pengukuran** (benchmark pola alokasi, 100 outfit × 8 item):

```
sebelum   409 allocs/op   149.512 B/op   31.771 ns/op
sesudah   103 allocs/op    82.216 B/op   19.354 ns/op
          −75%            −45%           −39%
```

### 4. SEDANG — Goroutine embedding tanpa plafon dan tanpa recover

`internal/service/embed.go`

`embedNameAsync` membuat satu goroutine per create/rename tanpa batas
konkurensi. Kalau penyedia embedding lambat (timeout penuh 10 detik) dan ada
lonjakan create, goroutine menumpuk — masing-masing memegang koneksi HTTP
keluar lalu mengantre di pool Postgres yang cuma sepuluh. Tumpukannya yang
memakan memori, bukan kerjanya. Selain itu goroutine ini di luar jangkauan
middleware `recoverPanic`, jadi panic di sana membunuh seluruh proses.

Pemakaian `context.Background()` di sana **benar** dan tidak diubah: memakai
context request akan membuat kerjanya gagal diam-diam begitu handler kembali.

**Status saat ini:** laten, belum aktif. `WithEmbedder` tidak pernah dipanggil
di luar test, jadi `s.embedder == nil` dan fungsinya langsung kembali.
Diperbaiki sekarang justru karena itu — sebelum embedder dicolok dan ketiga
masalahnya aktif serentak.

**Perbaikan:** semaphore berkapasitas 8 (di bawah `DB_MAX_CONNS` yang 10,
supaya kerja latar tidak pernah menghabiskan pool milik request yang sedang
ditunggu klien). Penuh berarti dilewati, bukan ditunggu — menunggu akan menahan
handler HTTP yang seharusnya sudah selesai, dan melewatkannya aman karena
kolomnya nullable dan bisa disapu ulang lewat `WHERE name_embedding IS NULL`.
Ditambah `recover` di goroutine-nya.

### 5. SEDANG — Shutdown normal dilaporkan sebagai crash

`cmd/server/main.go`

`SHUTDOWN_TIMEOUT` (10 detik) lebih pendek dari `WriteTimeout` (15 detik). Satu
request sah yang berjalan 12 detik saat SIGTERM datang sudah cukup membuat
`srv.Shutdown` mengembalikan `DeadlineExceeded` → `run()` mengembalikan error →
proses keluar dengan status 1. Orchestrator mencatat penghentian yang normal
sebagai crash, lalu memicu alarm untuk hal yang memang seharusnya terjadi.

**Perbaikan:** `DeadlineExceeded` saat shutdown dicatat sebagai peringatan dan
prosesnya keluar bersih. Resource tetap ditutup oleh `defer` yang sudah ada.
Error lain dari `Shutdown` tetap fatal.

Batas waktunya sengaja tidak dinaikkan: 10 detik dipilih supaya muat di
`terminationGracePeriodSeconds: 30` di manifest k8s, dan menaikkannya
memperlambat setiap rollout. Kalau nanti ada endpoint yang memang butuh lebih
lama, naikkan `SHUTDOWN_TIMEOUT` di atas `WriteTimeout` — jangan turunkan
`WriteTimeout`.

---

## Yang diperiksa dan sengaja TIDAK diubah

| Temuan | Lokasi | Alasan |
|---|---|---|
| `MemoryOutfits.views` tumbuh mengikuti trafik | `internal/store/memory.go` | Jalur dev saja. Jalur produksi menyimpannya di tabel `outfit_view`. Satu-satunya struktur in-memory yang tumbuh sebanding trafik, bukan sebanding data — proses dev yang berumur panjang memang akan membengkak, dan itu bisa diterima untuk penyimpanan yang datanya hilang saat proses berhenti |
| Dua serialisasi JSON penuh per `GET /v1/outfits` | `internal/store/cached/outfits.go` | Konsekuensi langsung dari desain cache yang sudah didokumentasikan. Menyimpan respons JSON jadi di Redis akan merusak pemisahan store/DTO dan membuat perubahan bentuk API meracuni entri lama. Ini harga lapisan cache, bukan bug |
| `unmarshalBody`: 5 alokasi per baris | `internal/store/postgres/outfits.go` | Bentuk JSON ditulis eksplisit supaya isi kolom tidak ikut berubah saat field di paket `model` diganti nama — alasan itu sah dan lebih berharga daripada 2 alokasi per baris. Ongkos dominannya `json.Unmarshal` berbasis refleksi, yang tidak hilang dengan merapikan struct |
| `append([]string(nil), f.OutfitIDs...)` untuk kunci cache | `internal/store/cached/outfits.go` | Salinannya memang perlu: `sort.Strings` in-place akan mengacak slice milik pemanggil yang detik berikutnya diteruskan ke `inner.List`. Satu alokasi per request pada jalur non-default |
| `MaxConnLifetime`/`MaxConnIdleTime` ada di struct tapi tak pernah diisi | `internal/store/postgres/postgres.go` | Default pgxpool (lifetime 1 jam, idle 30 menit) sudah sehat. Bukan bug memori, tapi konfigurasi mati yang menyesatkan — layak dibereskan, bukan sekarang |
| Redis `ConnMaxLifetime` tidak disetel | `internal/cache/redis.go` | Default go-redis tidak memensiunkan koneksi karena umur, tapi dial/read/write timeout sudah ada sehingga tidak ada koneksi menggantung. Dampaknya kecil untuk beban ini |
| Jalur tulis membaca outfit dua kali | `internal/service/outfit.go` | Memindahkan `ensureOwner` ke dalam closure `Update` akan mengubah semantik error (404/410 vs 403). Tidak sepadan untuk penghematan sebesar ini |

---

## Diperiksa dan memang bersih

- **Semua `pgx.Rows` ditutup.** Diverifikasi satu per satu di `apikeys.go`,
  `transactions.go`, `cashback.go`, `templates.go`, `outfits.go` — termasuk pada
  jalur error, karena helper `collect*` memasang `defer rows.Close()` di baris
  pertama.
- **Tidak ada N+1 di jalur panas.** `attachItems` dan `LikedBy` masing-masing
  satu query `= ANY($1)` untuk seluruh halaman; insert item memakai `pgx.Batch`.
- **Tidak ada `go doSomething(r.Context(), ...)`** di mana pun — anti-pola
  goroutine yang memakai context request yang sudah dibatalkan tidak ada di
  repo ini.
- **Tidak ada mutex yang dipegang sambil I/O.** Semua 40+ situs `Lock`/`RLock`
  murni operasi map in-memory. Callback yang dipanggil di bawah lock adalah
  closure validasi tanpa I/O.
- **Timeout HTTP server lengkap:** `ReadHeaderTimeout`, `ReadTimeout`,
  `WriteTimeout`, `IdleTimeout` keempat-empatnya terpasang.
- **Urutan penutupan resource benar** — closer dijalankan terbalik dari urutan
  buka, dan `errCh` berkapasitas 1 sehingga goroutine `ListenAndServe` tidak
  pernah tersangkut.
- **`internal/httpapi/dto.go`** — seluruh konstruktor DTO sudah
  `make([]T, 0, len(src))`.
- **`internal/store/cached`** — semua `Set` membawa TTL; tidak ada cache
  dalam-proses.
- **`internal/ratelimit`** — `pruneLocked` membuang jendela kedaluwarsa. Pola
  inilah yang dipinjam untuk memperbaiki temuan 1.

---

## Verifikasi

```
go vet ./...                  bersih
go test -race ./...           seluruh paket lulus
gofmt -l internal/ cmd/       bersih
```

Tes baru yang menahan temuan-temuan ini agar tidak kembali:

- `internal/idempotency/sweep_test.go` — 3 kasus (penyapuan bekerja, penyapuan
  diredam, yang masih berlaku tidak ikut terbuang)
- `internal/idempotency/idempotency_test.go` — 3 kasus dasar
- `internal/httpapi/idempotency_capture_test.go` — 4 kasus batas capture

## Tindak lanjut yang belum dikerjakan

1. **Tolak `MemoryStore` idempotensi saat `APP_ENV=production`**, meniru pola
   yang sudah dipakai untuk `AUTH_REQUIRED=false`. Penyapuan sudah menutup
   kebocorannya, tapi penyimpan idempotensi yang hilang saat restart tetap
   bukan yang diinginkan di produksi.
2. **Tunggu goroutine embedding saat shutdown.** Semaphore sudah membatasi
   jumlahnya, tapi `main` masih menutup pool Postgres tanpa menunggu kerja latar
   selesai. Butuh `WaitGroup` yang di-`Wait` antara `srv.Shutdown()` dan
   `backend.close()`. Belum mendesak karena embedder belum dipasang.
3. **Sambungkan `MaxConnLifetime`/`MaxConnIdleTime` ke `config.Load()`** atau
   hapus field-nya, supaya tidak ada konfigurasi mati yang menyesatkan pembaca.
4. **Profil `pprof` di beban sungguhan.** Semua angka di dokumen ini berasal
   dari pembacaan kode dan benchmark mikro. Sebelum mengejar temuan yang
   sengaja dilewati di atas, ukur dulu — kalau profil menunjuk ke
   `encoding/json`, penyebabnya kemungkinan besar lapisan cache, dan itu memang
   disengaja.
