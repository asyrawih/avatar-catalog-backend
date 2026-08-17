# Deploy ke Kubernetes

Padanan `docker-compose.yml` untuk cluster. Semua manifest ada di folder ini.

```
k8s/
  base/                 manifest inti, dipakai semua lingkungan
    namespace.yaml
    configmap-app.yaml  setelan non-rahasia (sejajar .env.example)
    secret-app.yaml     password Postgres (nilai dev); kunci API TIDAK di sini
    postgres.yaml       StatefulSet pgvector + PVC + Service headless
    redis.yaml          Deployment cache tanpa persistensi + Service
    api.yaml            Deployment + Service; initContainer tunggu-postgres + migrasi
    ingress.yaml        pengganti service caddy
  overlays/
    dev/                1 replika, resource kecil, host .localtest.me
    orbstack/           dev + Service LoadBalancer (OrbStack di macOS)
    prod/               3 replika, HPA, PDB, TLS cert-manager, ingress nginx
    k3s/                prod + ingress Traefik, dipas untuk satu node
      cluster-issuer.yaml  ClusterIssuer letsencrypt-prod (apply manual, scope cluster)
  deploy.sh             urutan apply yang benar
```

## Jalankan

```bash
./k8s/deploy.sh dev      # cluster lokal
./k8s/deploy.sh orbstack # OrbStack di macOS (dev + LoadBalancer)
./k8s/deploy.sh k3s     # k3s satu node (Traefik)
./k8s/deploy.sh prod    # cluster umum (ingress nginx)
```

Deploy ke k3s: pakai overlay `k3s` — base memakai `ingressClassName: nginx`
sedangkan k3s membawa Traefik. Panduan lengkapnya di
[docs/deploy-k8s.md](../docs/deploy-k8s.md).

Manual, kalau tidak mau pakai script:

```bash
kubectl apply -f k8s/base/namespace.yaml
kubectl create configmap avatar-catalog-db-init -n avatar-catalog --from-file=db/init
kubectl apply -k k8s/overlays/dev
```

ConfigMap `avatar-catalog-db-init` sengaja dibuat di luar kustomize: sumbernya
`db/init/*.sql` berada di luar direktori kustomization, dan kustomize menolak
membaca file di luar akarnya. Kalau isi `db/init` berubah, jalankan ulang
`deploy.sh` — perintah `kubectl create ... --dry-run | kubectl apply` di
dalamnya sudah idempoten.

## Cek

```bash
kubectl -n avatar-catalog get pods
kubectl -n avatar-catalog port-forward svc/avatar-catalog-api 8080:8080
curl localhost:8080/readyz   # 200 = Postgres & Redis tersambung
```

`/readyz` melaporkan tiap dependensi satu per satu, jadi kalau statusnya
`degraded` isi field `checks` menunjukkan bagian mana yang bermasalah.

## Yang perlu diganti sebelum produksi

1. **Image.** `images.newTag` di `overlays/prod/kustomization.yaml` masih
   `v0.1.0` dan registry-nya `ghcr.io/asyrawih/avatar-catalog-backend`. Build dan
   push dulu:
   ```bash
   docker build -t ghcr.io/asyrawih/avatar-catalog-backend:v0.1.0 .
   docker push ghcr.io/asyrawih/avatar-catalog-backend:v0.1.0
   ```
   Registry privat butuh `imagePullSecrets` di ServiceAccount namespace.
2. **Host.** `overlays/prod/patch-ingress.yaml` memakai
   `catalogv2.kelasmalam.app`. Ganti host dan pastikan DNS-nya mengarah ke
   ingress controller.
3. **Secret.** Lihat bagian berikut.
4. **StorageClass.** `volumeClaimTemplates` di `postgres.yaml` tidak menyebut
   `storageClassName`, jadi memakai default cluster. Sebutkan eksplisit kalau
   cluster punya beberapa kelas dan yang default bukan SSD.

## Secret di produksi

`base/secret-app.yaml` berisi password dev dalam plaintext dan ikut masuk git.
Jangan dipakai apa adanya. Dua jalan:

- **Timpa dari luar git** — biarkan file base-nya, lalu setelah apply:
  ```bash
  kubectl -n avatar-catalog create secret generic avatar-catalog-secret \
    --from-literal=POSTGRES_PASSWORD='...' \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n avatar-catalog rollout restart deployment/avatar-catalog-api
  ```
- **Sealed Secrets / External Secrets Operator** — hapus `secret-app.yaml` dari
  `base/kustomization.yaml` dan tambahkan resource terenkripsi di overlay prod.

Kunci API tidak ikut Secret ini: kuncinya hidup di tabel `api_key` sebagai
hash dan diterbitkan dengan `cmd/apikey`, jadi rotasi maupun pencabutan tidak
butuh redeploy. Setelah pod jalan:

```bash
kubectl -n avatar-catalog exec -it statefulset/avatar-catalog-db -- \
  psql -U avatar -d avatar_catalog -c 'SELECT key_id, name FROM api_key'
```

Untuk menerbitkan kunci, jalankan `cmd/apikey` dari mesin yang bisa menjangkau
Postgres (mis. lewat `kubectl port-forward svc/avatar-catalog-db 5432:5432`) —
lihat [docs/auth.md](../docs/auth.md).

Password Postgres disusun jadi `DATABASE_URL` di `api.yaml` lewat ekspansi
`$(VAR)`, jadi hindari karakter `@ : / ? # &`. Kalau terpaksa dipakai, timpa
`DATABASE_URL` utuh dengan patch di overlay.

## Beda dari docker compose

| compose | kubernetes |
|---|---|
| service `caddy` (profile edge) | Ingress + cert-manager |
| service `adminer` (profile tools) | tidak dibawa; pakai `kubectl port-forward svc/avatar-catalog-db 5432:5432` lalu psql/GUI dari lokal |
| `depends_on: service_healthy` | `readinessProbe` + `/readyz` — api start duluan lalu jadi Ready setelah dependensi hidup |
| volume `pgdata` | PVC 10Gi dari `volumeClaimTemplates` |
| `docker-compose.server.yml` (network Caddy eksternal) | tidak perlu; Service `avatar-catalog-api` sudah jadi nama stabil |

## Catatan operasional

- **Migrasi.** Sama seperti compose: `db/init/*.sql` hanya jalan saat PGDATA
  masih kosong. Perubahan skema setelah database berisi harus diterapkan
  sendiri (`kubectl exec` + `psql`, atau tambahkan Job migrasi).
- **Postgres single node.** `replicas: 1` di StatefulSet; menaikkannya tidak
  membuat replikasi. Untuk HA pakai operator (CloudNativePG/Zalando) atau
  Postgres terkelola, lalu hapus `postgres.yaml` dari base dan arahkan
  `DATABASE_URL` ke sana.
- **Redis boleh hilang.** Tanpa persistensi dan `maxmemory-policy allkeys-lru`,
  sesuai desain cache di aplikasi. Restart pod = cache dingin, bukan kehilangan
  data.
- **HPA** butuh metrics-server terpasang. Kalau tidak ada, hapus `hpa.yaml`
  dari `overlays/prod/kustomization.yaml`, jangan biarkan menggantung.
- **NetworkPolicy** sengaja tidak disertakan — efeknya bergantung CNI dan mudah
  memutus DNS kalau salah. Tambahkan sendiri kalau cluster memang menegakkannya.
