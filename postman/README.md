# Postman collection

```
avatar-catalog-api.postman_collection.json    35 request, 8 folder
avatar-catalog-api.postman_environment.json   variabel lingkungan lokal
build_collection.py                           pembangkit collection
```

## Pemasangan

1. Import kedua berkas JSON ke Postman.
2. Terbitkan kunci API — semua rute `/v1` menolak request tanpa kunci:

   ```bash
   export DATABASE_URL=postgres://avatar:avatar_dev_password@localhost:5432/avatar_catalog?sslmode=disable

   go run ./cmd/apikey issue --name postman       --role game-server --env test
   go run ./cmd/apikey issue --name postman-admin --role dashboard   --env test
   go run ./cmd/apikey issue --name postman-ai    --role ai          --env test
   ```

3. Isi environment: `apiKey` (game-server), `adminApiKey` (dashboard),
   `aiApiKey` (ai).

Kalau semua request menjawab `401`, kuncinya belum terisi — bukan server yang
rusak. Skrip pre-request collection memperingatkan ini di console.

## Menjalankan seluruh collection

Urutan foldernya sengaja: **Pembersihan** ada di paling akhir karena soft delete
membuat outfit menjawab `410` di semua endpoint lain. Menjalankan penghapusan
lebih awal akan menggagalkan folder Like & View.

Lewat Collection Runner, atau dari terminal:

```bash
npx newman run postman/avatar-catalog-api.postman_collection.json \
  -e postman/avatar-catalog-api.postman_environment.json
```

Variabel `outfitId`, `referenceId`, `txId`, `requestId`, dan `cursor` diisi
otomatis oleh skrip test dari respons sebelumnya, jadi folder bisa dijalankan
berurutan tanpa menyalin id secara manual.

## Tiga kunci, bukan satu

Folder **Autentikasi — contoh penolakan** berisi request yang *seharusnya*
gagal, supaya batas keamanannya bisa diperiksa sendiri alih-alih dipercaya:

| Request | Harapan | Yang dibuktikan |
|---|---|---|
| Tanpa kunci | `401` | API tidak terbuka |
| Kunci AI menulis | `403 insufficient_scope` | Kunci baca tidak bisa menulis |
| Dashboard kirim `X-User-Id` | `403 actor_assert_forbidden` | Hanya game server boleh mewakili pemain |
| Game server tuntaskan redeem | `403 insufficient_scope` | Kunci game server tidak menyentuh jalur uang keluar |
| `sort` ngawur | `400 invalid_sort` | Parameter salah ditolak, bukan jatuh ke bawaan |

## Catatan

- **Kunci API bersifat rahasia.** Environment yang ikut repo ini sengaja
  dikosongkan; jangan commit kunci yang sudah terisi. Kalau telanjur, cabut
  dengan `go run ./cmd/apikey revoke <keyId>` — berlaku seketika tanpa redeploy.
- Dua request cashback (**Tuntaskan request redeem**, **Tarik cashback**) hanya
  membuktikan scope-nya lulus, bukan bahwa datanya ada: keduanya butuh request
  redeem atau transaksi yang cashback-nya sudah ter-accrue. Saldo pemain contoh
  belum tentu mencapai minimum redeem (100 Robux).
- Collection ini dibangkitkan `build_collection.py` dari rute yang benar-benar
  terdaftar di `internal/httpapi/router.go`. Kalau menambah endpoint, ubah
  skripnya lalu jalankan ulang — jangan mengedit JSON-nya langsung.
