# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

# Salin manifest dulu supaya layer dependensi bisa di-cache.
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
# CLI operasional ikut dibawa: menerbitkan kunci dan membuat operator dashboard
# butuh DATABASE_URL, dan satu-satunya tempat kredensial itu sudah ada tanpa
# perlu disalin ke mana-mana adalah pod ini sendiri.
#
#   kubectl exec deploy/avatar-catalog-api -- /app/apikey list
#   kubectl exec -it deploy/avatar-catalog-api -- /app/dashboarduser create --email you@contoh.com
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/apikey ./cmd/apikey && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dashboarduser ./cmd/dashboarduser
# migrate membawa db/migrations di dalam binernya (go:embed), jadi image ini
# selalu berisi skema yang cocok dengan kodenya. Dijalankan sebelum server
# start — initContainer di Kubernetes, service `migrate` di compose.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /out/apikey /app/apikey
COPY --from=build /out/dashboarduser /app/dashboarduser
COPY --from=build /out/migrate /app/migrate

ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
