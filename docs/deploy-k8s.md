# Panduan Deploy: Kubernetes & k3s

Manifest-nya ada di [`k8s/`](../k8s); dokumen ini urutan kerjanya dari server
kosong sampai API bisa diakses publik. Referensi per-file ada di
[`k8s/README.md`](../k8s/README.md).

Target utama dokumen ini **k3s satu node**, karena itu rencana deploy-nya.
Bagian [Kubernetes umum](#kubernetes-umum) merangkum bedanya kalau nanti pindah
ke cluster lain (GKE/EKS/DOKS).

---

## Checklist

Urutannya penting — nomor 5 dan 6 sering terlewat, dan gejalanya terlihat
seperti deploy yang gagal padahal bukan.

- [ ] 1. k3s terpasang, `kubectl get nodes` jalan ([1.1](#11-pasang-k3s))
- [ ] 2. Image sudah di-push ke registry ([1.4](#14-siapkan-image))
- [ ] 3. Host di `patch-ingress.yaml` diganti, DNS mengarah ke server ([1.6](#16-domain-dan-tls))
- [ ] 4. `./k8s/deploy.sh k3s` — semua pod Running ([1.5](#15-deploy))
- [ ] 5. **Password Postgres diganti SEBELUM pod db pertama kali start** ([1.7](#17-secret-produksi))
- [ ] 6. **Kunci API diterbitkan** — tanpa ini `/v1` menjawab `401` ([1.8](#18-kunci-api))
- [ ] 7. Smoke test lulus ([1.9](#19-smoke-test))

## Ringkasan arsitektur

```
Internet ──▶ Ingress (Traefik di k3s) ──▶ Service avatar-catalog-api :8080
                                              │
                                              ├──▶ Service avatar-catalog-db    :5432  (StatefulSet + PVC)
                                              └──▶ Service avatar-catalog-redis :6379  (Deployment, tanpa PVC)
```

Semua di namespace `avatar-catalog`.

| Compose | Kubernetes |
|---|---|
| service `caddy` | Ingress controller cluster (Traefik bawaan k3s) |
| service `db` | StatefulSet `pgvector/pgvector:pg17` + PVC 10Gi |
| service `redis` | Deployment tanpa volume — cache boleh hilang |
| service `api` | Deployment + Service ClusterIP |
| `depends_on: service_healthy` | `readinessProbe` ke `/readyz` |

---

## OrbStack (macOS, pengembangan lokal)

OrbStack membawa Kubernetes sendiri dan **memakai image store yang sama dengan
Docker-nya** — jadi image hasil `docker build` langsung terlihat cluster, tanpa
registry sama sekali.

```bash
docker build -t ghcr.io/asyrawih/avatar-catalog-backend:dev .
./k8s/deploy.sh orbstack
```

Overlay `orbstack` = overlay `dev` ditambah Service LoadBalancer, supaya API
bisa dipanggil dari mac tanpa `port-forward`:

```bash
kubectl -n avatar-catalog get svc avatar-catalog-api-lb   # ambil EXTERNAL-IP
curl http://<EXTERNAL-IP>:8080/readyz
```

Service LoadBalancer diletakkan di overlay ini, bukan di `dev`, karena di
cluster lokal tanpa dukungan LoadBalancer ia akan menggantung `Pending`
selamanya.

Yang perlu diketahui soal OrbStack:

| Kebutuhan | Status |
|---|---|
| StorageClass | `local-path`, sudah default — PVC Postgres langsung jalan |
| LoadBalancer | Didukung; EXTERNAL-IP-nya IP node dan bisa dijangkau dari mac |
| Ingress controller | **Tidak ada.** Objek Ingress tetap dibuat tapi tidak dilayani — pakai LoadBalancer di atas, atau pasang ingress-nginx sendiri |
| metrics-server | **Tidak ada.** Overlay `dev`/`orbstack` tidak memakai HPA jadi tidak masalah; overlay `prod`/`k3s` butuh ini |

Menerbitkan kunci API butuh akses ke Postgres, dan database sengaja tidak
diekspos:

```bash
kubectl -n avatar-catalog port-forward statefulset/avatar-catalog-db 15432:5432 &
export DATABASE_URL="postgres://avatar:avatar_dev_password@localhost:15432/avatar_catalog?sslmode=disable"
go run ./cmd/apikey issue --name lokal --role game-server --env test
```

Menghapus semuanya: `kubectl delete namespace avatar-catalog`.

## Bagian 1 — k3s

### 1.1 Pasang k3s

Di server (Ubuntu/Debian, minimal 2 vCPU / 2 GB RAM):

```bash
curl -sfL https://get.k3s.io | sh -
sudo systemctl status k3s
```

Ambil kubeconfig supaya `kubectl` jalan tanpa sudo:

```bash
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown "$(id -u):$(id -g)" ~/.kube/config
kubectl get nodes
```

Untuk `kubectl` dari laptop, salin file itu ke lokal lalu ganti
`server: https://127.0.0.1:6443` jadi IP publik server. Port 6443 harus
terbuka — batasi ke IP kamu saja, jangan ke `0.0.0.0/0`.

### 1.2 Yang sudah dibawa k3s (dan yang belum)

| Kebutuhan | Status di k3s |
|---|---|
| Ingress controller | **Traefik**, terpasang otomatis. Dipakai lewat overlay `k3s` (lihat 1.3) |
| StorageClass | `local-path` (default). PVC Postgres langsung jalan |
| metrics-server | Terpasang otomatis, jadi HPA di overlay prod berfungsi |
| LoadBalancer | ServiceLB (klipper) — Service `LoadBalancer` dapat IP node |
| cert-manager | **Tidak ada.** Perlu dipasang sendiri kalau mau TLS otomatis |

Cek sendiri:

```bash
kubectl get ingressclass
kubectl get storageclass
kubectl -n kube-system get deploy metrics-server
```

### 1.3 Ingress: pakai overlay `k3s`

k3s membawa **Traefik**, sedangkan `k8s/base/ingress.yaml` menulis
`ingressClassName: nginx`. Kalau dipakai apa adanya, ingress-nya tidak pernah
dilayani (404) dan anotasi nginx diabaikan diam-diam — bukan error, jadi
gampang lolos dari perhatian.

Overlay `k8s/overlays/k3s/` sudah menangani itu, jadi **tidak ada yang perlu
kamu tulis sendiri**. Isinya: overlay `prod` (HPA, PDB, TLS) ditambah

| File | Kegunaan |
|---|---|
| `patch-ingress.yaml` | `ingressClassName: traefik`, buang anotasi nginx, pasang entrypoint `websecure` |
| `traefik-middleware.yaml` | Middleware `compress` — padanan `encode zstd gzip` di Caddyfile |
| `traefik-serverstransport.yaml` | `dialTimeout 5s` + `responseHeaderTimeout 30s` — padanan blok `transport http` di Caddyfile |
| `patch-api-service.yaml` | menyambungkan ServersTransport ke Service (di Traefik dirujuk dari Service, bukan Ingress) |
| `patch-hpa.yaml`, `patch-pdb.yaml` | dipas untuk satu node: HPA 2–6, PDB `minAvailable: 1` |

Deploy-nya:

```bash
./k8s/deploy.sh k3s
```

CRD Traefik (`Middleware`, `ServersTransport`) sudah ada begitu k3s terpasang.
Kalau k3s-nya lama dan masih Traefik v2, `apiVersion`-nya
`traefik.containo.us/v1alpha1`, bukan `traefik.io/v1alpha1` — cek dengan
`kubectl get crd | grep middlewares` dan sesuaikan dua file `traefik-*.yaml`.

#### Redirect HTTP ke HTTPS

Router di overlay ini hanya melayani entrypoint `websecure`. Middleware
`redirectScheme` sengaja **tidak** dipakai: dia berlaku juga untuk request yang
sudah HTTPS, jadi hasilnya redirect berputar. Di k3s redirect dipasang di level
entrypoint, sekali untuk seluruh cluster:

```yaml
# simpan sebagai traefik-redirect.yaml lalu: kubectl apply -f traefik-redirect.yaml
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
    ports:
      web:
        redirectTo:
          port: websecure
          priority: 10
```

k3s akan me-redeploy Traefik sendiri. File ini di luar `k8s/` karena
namespace-nya `kube-system` dan sifatnya konfigurasi cluster, bukan aplikasi.

Lewati bagian ini kalau TLS diterminasi di Cloudflare — redirect sudah
dikerjakan di sana.

#### Soal `X-Real-IP`

`Caddyfile` menyalin `CF-Connecting-IP` ke `X-Real-IP`. Traefik tidak punya
padanannya: `customRequestHeaders` hanya bisa mengisi nilai statis, tidak bisa
menyalin nilai header lain.

Sejauh ini itu tidak berdampak — kode aplikasi belum pernah membaca IP klien
(tidak ada rujukan ke `X-Real-IP`, `X-Forwarded-For`, maupun `RemoteAddr` di
`internal/` dan `cmd/`), dan `internal/ratelimit` belum disambungkan ke router.
Komentar di `Caddyfile` sendiri menyebutnya untuk kebutuhan "nanti".

Kalau nanti IP klien memang dipakai, dua jalan: baca `X-Forwarded-For` dari
aplikasi (Traefik mengisinya otomatis), atau ganti ke ingress-nginx —
`--disable traefik` saat install k3s, lalu pasang ingress-nginx dan pakai
overlay `prod` yang anotasinya sudah nginx.

### 1.4 Siapkan image

k3s memakai containerd, bukan Docker, jadi image hasil `docker build` di server
yang sama **tidak** otomatis terlihat.

**Lewat registry (disarankan):**

```bash
docker build -t ghcr.io/asyrawih/avatar-catalog-backend:v0.1.0 .
docker push ghcr.io/asyrawih/avatar-catalog-backend:v0.1.0
```

Registry privat butuh pull secret:

```bash
kubectl -n avatar-catalog create secret docker-registry ghcr \
  --docker-server=ghcr.io \
  --docker-username=asyrawih \
  --docker-password='<PAT dengan scope read:packages>'

kubectl -n avatar-catalog patch serviceaccount default \
  -p '{"imagePullSecrets":[{"name":"ghcr"}]}'
```

**Tanpa registry**, impor langsung ke containerd:

```bash
docker save ghcr.io/asyrawih/avatar-catalog-backend:v0.1.0 | sudo k3s ctr images import -
```

Kalau pakai cara ini, set `imagePullPolicy: IfNotPresent` (sudah default di
manifest) dan **jangan** pakai tag `latest` — Kubernetes selalu mencoba menarik
tag `latest` dari registry.

### 1.5 Deploy

Dari root repo di server (butuh `db/init/` ada, jadi clone repo-nya):

```bash
./k8s/deploy.sh k3s
```

Script itu, berurutan: buat namespace → buat ConfigMap `avatar-catalog-db-init`
dari `db/init/*.sql` → `kubectl apply -k` overlay → tunggu rollout db, redis,
api.

Skema yang ikut terpasang di database kosong:

| Berkas | Isi |
|---|---|
| `001_schema.sql` | 13 tabel ERD v3 |
| `002_seed.sql` | data contoh |
| `003_cashback.sql` | `cashback_entry`, `redeem_request`, `cashback_event` |
| `004_engagement.sql` | `outfit_like`, `outfit_view`, kolom `like_count`/`view_count` |
| `005_api_key.sql` | `api_key` |

Semuanya hanya dijalankan Postgres saat PGDATA masih kosong. Untuk database
yang sudah berisi, terapkan sendiri — lihat
[Perubahan skema database](#perubahan-skema-database).

Verifikasi:

```bash
kubectl -n avatar-catalog get pods
kubectl -n avatar-catalog port-forward svc/avatar-catalog-api 8080:8080 &
curl -s localhost:8080/readyz | jq
```

`/readyz` mengembalikan status per dependensi, jadi kalau `degraded` isi
`checks` menunjukkan Postgres atau Redis yang bermasalah.

> **`/v1` masih menjawab `401` di titik ini, dan itu benar.** `AUTH_REQUIRED`
> bawaannya `true`, sedangkan belum ada satu kunci pun di tabel `api_key`.
> Lanjut ke [1.8](#18-kunci-api). Yang boleh dipanggil tanpa kunci hanya
> `/healthz` dan `/readyz`.

### 1.6 Domain dan TLS

Arahkan A record domain ke IP server. Traefik/nginx di k3s sudah mendengarkan
port 80 dan 443 lewat ServiceLB.

Untuk sertifikat otomatis (dipakai `k8s/overlays/prod/patch-ingress.yaml`, yang
diwarisi overlay `k3s`, lewat
anotasi `cert-manager.io/cluster-issuer`):

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
kubectl -n cert-manager rollout status deploy/cert-manager-webhook
```

lalu buat ClusterIssuer bernama `letsencrypt-prod`:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: hanan@kelasmalam.app
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
      - http01:
          ingress:
            ingressClassName: traefik
```

Ganti juga host `catalogv2.kelasmalam.app` di `patch-ingress.yaml` kalau
domainnya berbeda.

Kalau TLS diterminasi di Cloudflare (mode Flexible/Full), lewati cert-manager:
hapus blok `tls:` dan anotasi cert-manager dari `patch-ingress.yaml`.

### 1.7 Secret produksi

`k8s/base/secret-app.yaml` berisi password dev dan ikut masuk git. Timpa
sesudah deploy:

```bash
kubectl -n avatar-catalog create secret generic avatar-catalog-secret \
  --from-literal=POSTGRES_PASSWORD="$(openssl rand -base64 24 | tr -d '/@:?#&+=')" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n avatar-catalog rollout restart deployment/avatar-catalog-api
```

Dua hal yang gampang menggigit:

- **Password harus diganti sebelum Postgres pertama kali start.** Setelah
  PGDATA terbentuk, mengubah Secret tidak mengubah password di dalam database —
  api akan gagal autentikasi. Kalau telanjur: `kubectl -n avatar-catalog exec -it
  statefulset/avatar-catalog-db -- psql -U avatar -c "ALTER USER avatar PASSWORD '...'"`.
- **Karakter `@ : / ? # &` dilarang** di password. Nilainya disusun jadi
  `DATABASE_URL` lewat ekspansi `$(VAR)` di `k8s/base/api.yaml`, tanpa
  URL-encoding. Perintah `openssl` di atas sudah membuang karakter itu.

### 1.8 Kunci API

Kunci konsumen (Roblox, dashboard, AI) **tidak** ada di Secret: kuncinya hidup
di tabel `api_key` sebagai hash, jadi rotasi dan pencabutan berlaku seketika
tanpa redeploy. Terbitkan dari mesin yang bisa menjangkau Postgres:

```bash
kubectl -n avatar-catalog port-forward svc/avatar-catalog-db 5432:5432 &

export DATABASE_URL="postgres://avatar:<password>@localhost:5432/avatar_catalog?sslmode=disable"
go run ./cmd/apikey issue --name roblox-game-server-prod --role game-server --expires 90d
go run ./cmd/apikey list
```

Token utuh hanya muncul sekali. Untuk Roblox, simpan sebagai Secret di Creator
Hub — panduan lengkapnya di [docs/auth.md](auth.md).

`AUTH_REQUIRED` sudah `"true"` di `k8s/base/configmap-app.yaml`. Jangan diubah
jadi `"false"` di lingkungan yang bisa dijangkau dari luar; server menolak
start kalau `false` dipasang bersama `APP_ENV=production`.

Terbitkan minimal satu kunci per konsumen, jangan dipakai bersama — kunci
bersama tidak bisa dicabut sendiri-sendiri saat salah satunya bocor:

```bash
go run ./cmd/apikey issue --name roblox-game-server-prod --role game-server --expires 90d
go run ./cmd/apikey issue --name dashboard-internal     --role dashboard   --expires 365d
go run ./cmd/apikey issue --name ai-trainer             --role ai          --expires 90d
```

### 1.9 Smoke test

Enam pemeriksaan ini membuktikan deploy-nya benar-benar hidup, bukan sekadar
pod-nya Running. Jalankan dari luar cluster memakai domain sungguhan.

```bash
HOST=https://catalogv2.kelasmalam.app
KEY=acb_live_...        # kunci role game-server dari langkah 1.8

st() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

echo "1. healthz tanpa kunci       : $(st $HOST/healthz)                       # ingin 200"
echo "2. readyz  tanpa kunci       : $(st $HOST/readyz)                        # ingin 200"
echo "3. /v1 tanpa kunci           : $(st $HOST/v1/outfits)                    # ingin 401"
echo "4. /v1 dengan kunci          : $(st -H "Authorization: Bearer $KEY" $HOST/v1/outfits)   # ingin 200"
echo "5. TLS sah                   : $(curl -s -o /dev/null -w '%{http_code}' --proto '=https' $HOST/healthz)  # ingin 200"
echo "6. game-server tolak redeem  : $(st -X PATCH -H "Authorization: Bearer $KEY" \
       -H 'Content-Type: application/json' -d '{"status":"completed"}' \
       $HOST/v1/cashback/redeems/req_x)   # ingin 403"
```

Nomor 3 dan 6 sama pentingnya dengan nomor 4. Nomor 3 membuktikan API tidak
terbuka tanpa kunci; nomor 6 membuktikan kunci game server tidak bisa
menyentuh jalur uang keluar. Kalau nomor 6 menjawab `400` alih-alih `403`,
kunci yang dipakai bukan role `game-server` — periksa dengan `apikey list`.

Setelah itu, pastikan `readyz` melaporkan kedua dependensi sehat:

```bash
curl -s $HOST/readyz | jq
# {"status":"ok","checks":{"postgres":"ok","redis":"ok"}}
```

---

## Bagian 2 — Operasional

### Rilis versi baru

```bash
docker build -t ghcr.io/asyrawih/avatar-catalog-backend:v0.2.0 .
docker push ghcr.io/asyrawih/avatar-catalog-backend:v0.2.0

# di server
# tag image dipegang overlay prod; overlay k3s mewarisinya
sed -i 's/newTag: .*/newTag: v0.2.0/' k8s/overlays/prod/kustomization.yaml
kubectl apply -k k8s/overlays/k3s
kubectl -n avatar-catalog rollout status deploy/avatar-catalog-api
```

Rollback:

```bash
kubectl -n avatar-catalog rollout undo deploy/avatar-catalog-api
```

`maxUnavailable: 0` di Deployment, jadi rollout tidak pernah menurunkan
kapasitas — pod baru harus Ready dulu sebelum yang lama dimatikan. Pakai tag
versi, bukan `latest`; dengan `latest` perintah rollback tidak punya arti.

### Perubahan skema database

Skrip `db/init/*.sql` **hanya** dieksekusi saat PGDATA masih kosong — sama
seperti di docker compose. Database yang sudah berisi tidak akan ikut berubah
saat kamu deploy versi baru, dan gejalanya adalah api gagal start atau
menjawab `500` karena kolom yang dicarinya tidak ada.

Terapkan sendiri, berurutan:

```bash
for f in db/init/004_engagement.sql db/init/005_api_key.sql; do
  kubectl -n avatar-catalog exec -i statefulset/avatar-catalog-db -- \
    psql -U avatar -d avatar_catalog -v ON_ERROR_STOP=1 < "$f"
done
```

`004` dan `005` ditulis idempoten (`IF NOT EXISTS`), jadi aman dijalankan
berulang dan aman dijalankan pada database yang sudah punya sebagian tabelnya.
`001`–`003` **tidak** — jangan dijalankan ulang pada database berisi.

Urutan yang benar saat merilis versi yang butuh skema baru: terapkan migrasi
dulu, baru rollout api. Terbalik berarti pod baru berjalan di atas skema lama.
Kalau nanti sering, tambahkan Job migrasi yang jalan sebelum rollout.

### Ubah konfigurasi non-rahasia

Edit `k8s/base/configmap-app.yaml` (atau patch di overlay), lalu:

```bash
kubectl apply -k k8s/overlays/k3s
kubectl -n avatar-catalog rollout restart deploy/avatar-catalog-api
```

Restart-nya wajib: pod tidak membaca ulang ConfigMap yang dipakai lewat `env`.

### Backup Postgres

`local-path` di k3s menulis ke disk node — hilang kalau node hilang. Backup
harus ke luar server:

```bash
kubectl -n avatar-catalog exec statefulset/avatar-catalog-db -- \
  pg_dump -U avatar -d avatar_catalog --format=custom > backup-$(date +%F).dump
```

Restore:

```bash
kubectl -n avatar-catalog exec -i statefulset/avatar-catalog-db -- \
  pg_restore -U avatar -d avatar_catalog --clean --if-exists < backup-2026-08-11.dump
```

Jadwalkan lewat cron di host, atau CronJob yang mengirim ke object storage.

### Log dan debug

```bash
kubectl -n avatar-catalog logs -f deploy/avatar-catalog-api
kubectl -n avatar-catalog logs deploy/avatar-catalog-api --previous   # setelah crash
kubectl -n avatar-catalog describe pod -l app.kubernetes.io/name=api
kubectl -n avatar-catalog get events --sort-by=.lastTimestamp | tail -20
```

Image api distroless — **tidak ada shell**, jadi `kubectl exec` ke pod api
tidak bisa. Untuk memeriksa dari dalam cluster, jalankan pod sementara:

```bash
kubectl -n avatar-catalog run debug --rm -it --image=alpine --restart=Never -- sh
# di dalamnya: apk add curl; curl avatar-catalog-api:8080/readyz
```

Masuk ke database:

```bash
kubectl -n avatar-catalog exec -it statefulset/avatar-catalog-db -- psql -U avatar -d avatar_catalog
```

Adminer tidak dibawa ke Kubernetes. Untuk GUI, forward port lalu sambungkan
dari lokal:

```bash
kubectl -n avatar-catalog port-forward svc/avatar-catalog-db 5432:5432
```

---

## Bagian 3 — Masalah yang sering muncul

| Gejala | Sebab dan penanganan |
|---|---|
| Pod `db` **Pending** | PVC belum terikat. `kubectl get pvc -n avatar-catalog`. Di k3s pastikan `local-path` ada dan disk node cukup |
| Pod `db` **CreateContainerConfigError** | ConfigMap `avatar-catalog-db-init` belum dibuat. Jalankan `./k8s/deploy.sh`, jangan `kubectl apply -k` saja |
| Pod `api` **CrashLoopBackOff** | Cek `logs --previous`. Paling sering `DATABASE_URL` salah karena password punya karakter khusus |
| Pod `api` Running tapi tidak Ready | `/readyz` gagal. Port-forward lalu `curl localhost:8080/readyz` — isi `checks` menunjuk Postgres atau Redis |
| **ImagePullBackOff** | Tag belum ada di registry, atau pull secret belum terpasang (lihat 1.4) |
| Ingress 404 | `ingressClassName` tidak cocok dengan controller yang jalan. `kubectl get ingressclass`, lalu lihat 1.3 |
| Semua `/v1` **401** | Belum ada kunci API, atau kunci yang dipakai salah/dicabut. `apikey list`; terbitkan dengan 1.8. Ini juga jawaban yang benar untuk request tanpa `Authorization` |
| **403** `insufficient_scope` | Kunci sah tapi role-nya tidak punya izin itu — mis. kunci `ai` mencoba menulis. Lihat role di `apikey roles` |
| **403** `actor_assert_forbidden` | Kunci tanpa `actor:assert` mengirim `X-User-Id`. Hanya kunci `game-server` yang boleh bertindak atas nama pemain |
| Pod `api` **CrashLoopBackOff** setelah upgrade | Skema belum dimigrasi. Terapkan `004`/`005` lalu rollout ulang |
| Sertifikat tidak terbit | `kubectl -n avatar-catalog describe certificate`. Umumnya DNS belum mengarah, atau port 80 tertutup sehingga HTTP-01 gagal |
| HPA `<unknown>/70%` | metrics-server belum siap. `kubectl -n kube-system get deploy metrics-server` |
| Data hilang setelah redeploy | PVC ikut terhapus. `kubectl delete -k` **tidak** menghapus PVC dari `volumeClaimTemplates`, tapi `kubectl delete pvc` iya. Cek dulu sebelum menghapus apa pun |

---

## Kubernetes umum

Kalau nanti pindah dari k3s ke cluster terkelola, yang berubah:

- **Ingress.** Manifest base sudah menulis `ingressClassName: nginx`, jadi cocok
  begitu ingress-nginx terpasang. Cloud LB punya anotasi sendiri (GKE:
  `kubernetes.io/ingress.class`, AWS: ALB controller).
- **StorageClass.** Sebutkan eksplisit di `volumeClaimTemplates` pada
  `k8s/base/postgres.yaml` — default cluster belum tentu SSD.
- **Postgres.** `replicas: 1` dan tidak ada replikasi. Untuk produksi serius,
  pakai Postgres terkelola (Cloud SQL/RDS) atau operator seperti CloudNativePG:
  hapus `postgres.yaml` dari `k8s/base/kustomization.yaml`, lalu arahkan
  `DATABASE_URL` ke endpoint baru lewat patch di overlay.
- **PodDisruptionBudget** di overlay prod (`minAvailable: 2`) baru berguna kalau
  node lebih dari satu. Di k3s single node dia tidak menghalangi apa pun.

---

## Rujukan

- Manifest per-file: [`k8s/README.md`](../k8s/README.md)
- Variabel environment: [`.env.example`](../.env.example) dan
  `internal/config/config.go`
- Endpoint API: [`README.md`](../README.md)
