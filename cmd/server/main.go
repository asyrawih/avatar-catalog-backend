// Command server menjalankan HTTP API katalog avatar.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/cache"
	"github.com/hanan/avatar-catalog-backend/internal/config"
	"github.com/hanan/avatar-catalog-backend/internal/httpapi"
	"github.com/hanan/avatar-catalog-backend/internal/idempotency"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
	"github.com/hanan/avatar-catalog-backend/internal/store/cached"
	"github.com/hanan/avatar-catalog-backend/internal/store/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server berhenti karena error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStartup()

	backend, err := openBackend(startupCtx, cfg, logger)
	if err != nil {
		return err
	}
	defer backend.close()

	cashback := service.NewCashback(backend.cashback)
	deps := httpapi.Deps{
		Outfits:      service.NewOutfits(backend.outfits, backend.templates),
		Transactions: service.NewTransactions(backend.transactions, cashback),
		Cashback:     cashback,
		Templates:    service.NewTemplates(backend.templates),
		Idempotency:  backend.idempotency,
		Auth:         newAuthenticator(cfg, logger),
		Logger:       logger,
		Readiness:    backend.readiness,
	}

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           httpapi.NewRouter(deps),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server berjalan", "addr", srv.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("sinyal shutdown diterima")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("server berhenti dengan bersih")
	return nil
}

// backend menyatukan seluruh dependensi penyimpanan beserta cara menutupnya.
type backend struct {
	outfits      store.Outfits
	transactions store.Transactions
	cashback     store.Cashback
	templates    store.Templates
	idempotency  idempotency.Store
	readiness    map[string]func(context.Context) error
	closers      []func()
}

func (b *backend) close() {
	// Tutup terbalik dari urutan pembukaan.
	for i := len(b.closers) - 1; i >= 0; i-- {
		b.closers[i]()
	}
}

// openBackend memilih penyimpanan sesuai konfigurasi: Postgres bila
// DATABASE_URL diisi, in-memory bila tidak; ditambah cache Redis bila
// REDIS_URL diisi.
func openBackend(ctx context.Context, cfg config.Config, logger *slog.Logger) (*backend, error) {
	b := &backend{readiness: make(map[string]func(context.Context) error)}

	// --- cache dan penyimpan kunci idempotensi ---
	var cacheLayer cache.Cache = cache.Noop{}
	b.idempotency = idempotency.NewMemoryStore(cfg.IdempotencyTTL)

	if cfg.RedisURL != "" {
		redisCache, err := cache.OpenRedis(ctx, cfg.RedisURL, cfg.CachePrefix)
		if err != nil {
			return nil, err
		}
		cacheLayer = redisCache
		b.idempotency = idempotency.NewRedisStore(redisCache.Client(), redisCache.Prefix(), cfg.IdempotencyTTL, logger)
		b.readiness["redis"] = redisCache.Ping
		b.closers = append(b.closers, func() { _ = redisCache.Close() })
		logger.Info("cache redis aktif", "ttl", cfg.CacheTTL.String(), "prefix", cfg.CachePrefix)
	} else {
		logger.Warn("cache nonaktif", "catatan", "isi REDIS_URL supaya baca outfit tidak selalu memukul database")
	}

	// --- penyimpanan utama ---
	if cfg.DatabaseURL != "" {
		pool, err := postgres.Open(ctx, cfg.DatabaseURL, postgres.PoolConfig{
			MaxConns:       int32(cfg.DBMaxConns),
			MinConns:       int32(cfg.DBMinConns),
			ConnectTimeout: cfg.DBConnectTimeout,
		})
		if err != nil {
			return nil, err
		}

		b.outfits = postgres.NewOutfits(pool)
		b.transactions = postgres.NewTransactions(pool)
		b.cashback = postgres.NewCashback(pool)
		b.templates = postgres.NewTemplates(pool)
		b.readiness["postgres"] = pool.Ping
		b.closers = append(b.closers, pool.Close)
		logger.Info("penyimpanan postgres aktif", "max_conns", cfg.DBMaxConns)
	} else {
		outfitStore := store.NewMemoryOutfits()
		templateStore := store.NewMemoryTemplates()
		if cfg.SeedData {
			store.SeedData(templateStore, outfitStore)
		}

		txStore := store.NewMemoryTransactions()
		b.outfits = outfitStore
		b.transactions = txStore
		b.cashback = store.NewMemoryCashback(txStore)
		b.templates = templateStore
		logger.Warn("penyimpanan in-memory aktif", "catatan", "data hilang saat proses berhenti; isi DATABASE_URL untuk memakai postgres")
	}

	// --- lapisan cache di atas penyimpanan ---
	// Dibungkus hanya kalau ada cache sungguhan, supaya jalur tanpa Redis
	// tidak melewati kode yang pasti selalu miss.
	if _, isNoop := cacheLayer.(cache.Noop); !isNoop {
		b.outfits = cached.NewOutfits(b.outfits, cacheLayer, cfg.CacheTTL, logger)
	}
	return b, nil
}

// newAuthenticator memilih cara autentikasi berdasarkan konfigurasi.
//
// Autentikasi pemain sungguhan belum dikerjakan; tanpa AUTH_TOKENS server
// menerima semua request dan hanya membaca identitas dari header.
func newAuthenticator(cfg config.Config, logger *slog.Logger) httpapi.Authenticator {
	if len(cfg.AuthTokens) == 0 {
		logger.Warn("autentikasi nonaktif", "catatan", "isi AUTH_TOKENS untuk mewajibkan Bearer token")
		return httpapi.UnverifiedActorAuth{}
	}
	logger.Info("autentikasi token layanan aktif", "jumlah_token", len(cfg.AuthTokens))
	return httpapi.NewStaticTokenAuth(cfg.AuthTokens)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
