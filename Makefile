.PHONY: run build test test-integration check fmt vet tidy clean up down logs psql redis-cli db-reset caddy-reload

BINARY := bin/server

run:
	go run ./cmd/server

build:
	go build -o $(BINARY) ./cmd/server

test:
	go test ./...

# Butuh `docker compose up -d db` dan database sekali pakai — test ini
# mengosongkan tabel sebelum jalan.
TEST_DB ?= postgres://avatar:avatar_dev_password@localhost:$${POSTGRES_PORT:-5432}/avatar_catalog_test?sslmode=disable

test-integration:
	docker compose exec -T db psql -U $${POSTGRES_USER:-avatar} -d postgres -c "CREATE DATABASE avatar_catalog_test" || true
	docker compose exec -T db psql -U $${POSTGRES_USER:-avatar} -d avatar_catalog_test -f /docker-entrypoint-initdb.d/001_schema.sql
	TEST_DATABASE_URL="$(TEST_DB)" go test ./internal/store/postgres/... -v

# check adalah gerbang yang sama dengan CI.
check: fmt vet test

fmt:
	gofmt -l -w .

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	go clean
	rm -rf bin

# --- docker ---

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f api

psql:
	docker compose exec db psql -U $${POSTGRES_USER:-avatar} -d $${POSTGRES_DB:-avatar_catalog}

redis-cli:
	docker compose exec redis redis-cli

# Terapkan perubahan Caddyfile tanpa memutus koneksi yang sedang jalan.
caddy-reload:
	docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile

# Buang volume supaya skrip di db/init dijalankan ulang dari awal.
db-reset:
	docker compose down -v
	docker compose up -d db
