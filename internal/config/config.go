// Package config memuat konfigurasi runtime dari environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config berisi seluruh setelan yang dibaca saat server start.
type Config struct {
	Env             string
	Host            string
	Port            int
	LogLevel        string
	ShutdownTimeout time.Duration

	// DatabaseURL adalah DSN Postgres. Kosong berarti server memakai
	// penyimpanan in-memory — berguna untuk uji cepat tanpa docker.
	DatabaseURL      string
	DBMaxConns       int
	DBMinConns       int
	DBConnectTimeout time.Duration

	// RedisURL menyalakan cache baca dan penyimpan kunci idempotensi.
	// Kosong berarti tanpa cache; API tetap berfungsi, hanya lebih sering
	// memukul database.
	RedisURL    string
	CacheTTL    time.Duration
	CachePrefix string

	// SeedData mengisi penyimpanan in-memory dengan data contoh yang sama
	// dengan mock Postman. Diabaikan saat DatabaseURL diisi, karena isi awal
	// Postgres datang dari skrip di db/init.
	SeedData bool

	// IdempotencyTTL adalah umur simpan hasil POST per Idempotency-Key.
	IdempotencyTTL time.Duration

	// SyncRunLimit dan SyncRunWindow membatasi laporan sync per source.
	SyncRunLimit  int
	SyncRunWindow time.Duration

	// AuthTokens adalah daftar Bearer token layanan yang diterima. Kosong
	// berarti autentikasi belum diaktifkan.
	AuthTokens []string
}

// Load membaca environment dan menerapkan nilai bawaan.
func Load() (Config, error) {
	port, err := envInt("PORT", 8080)
	if err != nil {
		return Config{}, err
	}
	shutdown, err := envDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	idempotencyTTL, err := envDuration("IDEMPOTENCY_TTL", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	syncLimit, err := envInt("SYNC_RUN_LIMIT", 5)
	if err != nil {
		return Config{}, err
	}
	syncWindow, err := envDuration("SYNC_RUN_WINDOW", time.Minute)
	if err != nil {
		return Config{}, err
	}
	seed, err := envBool("SEED_DATA", true)
	if err != nil {
		return Config{}, err
	}
	dbMaxConns, err := envInt("DB_MAX_CONNS", 10)
	if err != nil {
		return Config{}, err
	}
	dbMinConns, err := envInt("DB_MIN_CONNS", 2)
	if err != nil {
		return Config{}, err
	}
	dbConnectTimeout, err := envDuration("DB_CONNECT_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	cacheTTL, err := envDuration("CACHE_TTL", time.Minute)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Env:             envString("APP_ENV", "development"),
		Host:            envString("HOST", "0.0.0.0"),
		Port:            port,
		LogLevel:        envString("LOG_LEVEL", "info"),
		ShutdownTimeout: shutdown,

		DatabaseURL:      envString("DATABASE_URL", ""),
		DBMaxConns:       dbMaxConns,
		DBMinConns:       dbMinConns,
		DBConnectTimeout: dbConnectTimeout,

		RedisURL:    envString("REDIS_URL", ""),
		CacheTTL:    cacheTTL,
		CachePrefix: envString("CACHE_PREFIX", "avatar-catalog"),

		SeedData:       seed,
		IdempotencyTTL: idempotencyTTL,
		SyncRunLimit:   syncLimit,
		SyncRunWindow:  syncWindow,
		AuthTokens:     envList("AUTH_TOKENS"),
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("config: PORT %d di luar rentang", cfg.Port)
	}
	if cfg.SyncRunLimit < 1 {
		return Config{}, fmt.Errorf("config: SYNC_RUN_LIMIT harus minimal 1")
	}
	return cfg, nil
}

// Addr adalah alamat listen HTTP server.
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func envString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envList(key string) []string {
	raw := os.Getenv(key)
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s harus berupa angka: %w", key, err)
	}
	return n, nil
}

func envBool(key string, fallback bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("config: %s harus berupa boolean: %w", key, err)
	}
	return b, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s harus berupa durasi: %w", key, err)
	}
	return d, nil
}
