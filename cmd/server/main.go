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

	authenticator, err := newAuthenticator(cfg, backend.apiKeys, logger)
	if err != nil {
		return err
	}

	// Login dashboard berdiri di samping kunci API: SessionAuth memeriksa
	// cookie sesi lebih dulu, lalu jatuh ke kunci. Cookie ditandai Secure di
	// mana pun kecuali development, karena di luar itu dashboard selalu
	// dilayani lewat HTTPS.
	dashboardAuth := service.NewDashboardAuth(backend.dashUsers)
	cookieSecure := cfg.Env != "development"
	authenticator = httpapi.NewSessionAuth(dashboardAuth, authenticator,
		httpapi.NewCookieConfig(cookieSecure), logger)

	cashback := service.NewCashback(backend.cashback)
	deps := httpapi.Deps{
		Outfits:       service.NewOutfits(backend.outfits, backend.templates),
		Transactions:  service.NewTransactions(backend.transactions, cashback),
		Cashback:      cashback,
		Templates:     service.NewTemplates(backend.templates),
		APIKeys:       service.NewAPIKeys(backend.apiKeys),
		DashboardAuth: dashboardAuth,
		CookieSecure:  cookieSecure,
		Idempotency:   backend.idempotency,
		Auth:          authenticator,
		RateLimit:     newRateLimiter(cfg, logger),
		Logger:        logger,
		Readiness:     backend.readiness,
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
		// Kehabisan waktu saat shutdown BUKAN kegagalan proses.
		//
		// SHUTDOWN_TIMEOUT (10 detik) lebih pendek dari WriteTimeout (15
		// detik), jadi satu request sah yang berjalan lama sudah cukup membuat
		// Shutdown menyerah. Kalau itu dikembalikan sebagai error, proses keluar
		// dengan status 1 dan orchestrator mencatat penghentian yang normal
		// sebagai crash — lalu memicu alarm untuk hal yang memang seharusnya
		// terjadi. Resource tetap ditutup oleh defer di atas.
		if errors.Is(err, context.DeadlineExceeded) {
			logger.Warn("shutdown kehabisan waktu; koneksi yang tersisa diputus paksa",
				"timeout", cfg.ShutdownTimeout.String())
			return nil
		}
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
	apiKeys      store.APIKeys
	dashUsers    store.DashboardUsers
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
		b.apiKeys = postgres.NewAPIKeys(pool)
		b.dashUsers = postgres.NewDashboardUsers(pool)
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
		b.apiKeys = store.NewMemoryAPIKeys()
		b.dashUsers = store.NewMemoryDashboardUsers()
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
// Kunci API hidup di tabel api_key, jadi menerbitkan dan mencabutnya tidak
// perlu redeploy — lihat cmd/apikey. AUTH_REQUIRED=false hanya untuk
// pengembangan lokal, dan ditolak di produksi.
func newAuthenticator(cfg config.Config, keys store.APIKeys, logger *slog.Logger) (httpapi.Authenticator, error) {
	if !cfg.AuthRequired {
		if cfg.Env == "production" {
			return nil, errors.New("config: AUTH_REQUIRED=false ditolak saat APP_ENV=production")
		}
		logger.Warn("autentikasi nonaktif",
			"catatan", "semua request diterima dan X-User-Id dipercaya apa adanya; hanya untuk pengembangan")
		return httpapi.UnverifiedActorAuth{}, nil
	}
	if keys == nil {
		return nil, errors.New("config: AUTH_REQUIRED=true butuh penyimpanan kunci")
	}
	logger.Info("autentikasi kunci API aktif")
	return httpapi.NewKeyAuth(keys, logger), nil
}

// newRateLimiter merangkai pembatas request per kunci API.
//
// Batasnya per proses: tiap replika menghitung sendiri, jadi batas efektif satu
// kunci adalah RATE_LIMIT_PER_SECOND dikali jumlah replika. Hitungan bersama
// lintas replika butuh Redis, dan itu belum dikerjakan — lihat docs/auth.md.
func newRateLimiter(cfg config.Config, logger *slog.Logger) httpapi.Limiter {
	if cfg.RateLimitPerSecond <= 0 {
		logger.Warn("pembatas request nonaktif",
			"catatan", "isi RATE_LIMIT_PER_SECOND supaya satu konsumen tidak bisa menghabiskan kapasitas yang lain")
		return nil
	}
	logger.Info("pembatas request aktif",
		"per_detik_per_kunci", cfg.RateLimitPerSecond, "catatan", "batas dihitung per proses, bukan per cluster")
	return httpapi.NewRateLimiter(cfg.RateLimitPerSecond)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
