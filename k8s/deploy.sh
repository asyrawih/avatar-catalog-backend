#!/usr/bin/env bash
# Deploy avatar-catalog ke Kubernetes.
#
#   ./k8s/deploy.sh dev     cluster lokal
#   ./k8s/deploy.sh k3s     k3s satu node, ingress Traefik
#   ./k8s/deploy.sh prod    cluster umum, ingress nginx
#
# ConfigMap skema database dibuat terpisah dari kustomize karena sumbernya
# ada di db/init — di luar direktori kustomization, dan kustomize menolak
# membaca file di luar akarnya.
set -euo pipefail

OVERLAY="${1:-dev}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAMESPACE="avatar-catalog"

if [[ ! -d "$ROOT/k8s/overlays/$OVERLAY" ]]; then
	echo "overlay tidak dikenal: $OVERLAY (pilih: dev, k3s, prod)" >&2
	exit 1
fi

echo "==> namespace $NAMESPACE"
kubectl apply -f "$ROOT/k8s/base/namespace.yaml"

# Skrip initdb hanya dieksekusi Postgres saat PGDATA masih kosong, tapi
# ConfigMap-nya harus sudah ada sebelum pod db dijadwalkan.
echo "==> configmap avatar-catalog-db-init (dari db/init)"
kubectl create configmap avatar-catalog-db-init \
	--namespace "$NAMESPACE" \
	--from-file="$ROOT/db/init" \
	--dry-run=client -o yaml | kubectl apply -f -

echo "==> apply overlay $OVERLAY"
kubectl apply -k "$ROOT/k8s/overlays/$OVERLAY"

echo "==> menunggu rollout"
kubectl -n "$NAMESPACE" rollout status statefulset/avatar-catalog-db --timeout=5m
kubectl -n "$NAMESPACE" rollout status deployment/avatar-catalog-redis --timeout=2m
kubectl -n "$NAMESPACE" rollout status deployment/avatar-catalog-api --timeout=5m

echo
echo "selesai. cek kesiapan dependensi:"
echo "  kubectl -n $NAMESPACE port-forward svc/avatar-catalog-api 8080:8080"
echo "  curl localhost:8080/readyz"
