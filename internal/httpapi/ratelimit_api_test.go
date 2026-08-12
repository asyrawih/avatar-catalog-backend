package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/httpapi"
	"github.com/hanan/avatar-catalog-backend/internal/idempotency"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// countingLimiter mencatat kunci yang dibatasi, supaya test bisa memastikan
// pembatas memakai keyId — bukan alamat IP — tanpa menunggu jendela sungguhan.
type countingLimiter struct {
	mu      sync.Mutex
	limit   int
	counts  map[string]int
	lastKey string
}

func newCountingLimiter(limit int) *countingLimiter {
	return &countingLimiter{limit: limit, counts: map[string]int{}}
}

func (c *countingLimiter) Allow(key string) (bool, int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastKey = key
	c.counts[key]++
	if c.counts[key] > c.limit {
		return false, 7
	}
	return true, 0
}

func (c *countingLimiter) key() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastKey
}

// limitedFixture adalah server dengan autentikasi kunci DAN pembatas aktif.
type limitedFixture struct {
	srv     *httptest.Server
	keys    *store.MemoryAPIKeys
	limiter *countingLimiter
	token   string
}

func newLimitedServer(t *testing.T, limit int) *limitedFixture {
	t.Helper()

	outfits := store.NewMemoryOutfits()
	templates := store.NewMemoryTemplates()
	transactions := store.NewMemoryTransactions()
	store.SeedData(templates, outfits)

	keys := store.NewMemoryAPIKeys()
	limiter := newCountingLimiter(limit)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cashback := service.NewCashback(store.NewMemoryCashback(transactions))
	handler := httpapi.NewRouter(httpapi.Deps{
		Outfits:      service.NewOutfits(outfits, templates),
		Transactions: service.NewTransactions(transactions, cashback),
		Cashback:     cashback,
		Templates:    service.NewTemplates(templates),
		Idempotency:  idempotency.NewMemoryStore(time.Hour),
		Auth:         httpapi.NewKeyAuth(keys, logger),
		RateLimit:    limiter,
		Logger:       logger,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	fx := &limitedFixture{srv: srv, keys: keys, limiter: limiter}
	token, err := auth.Generate(auth.EnvTest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	err = keys.Create(context.Background(), auth.Key{
		KeyID:     token.KeyID,
		Hash:      token.Hash,
		Name:      "dibatasi",
		Scopes:    auth.Roles["game-server"],
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	fx.token = token.Secret
	return fx
}

func TestRequestBerlebihDijawab429(t *testing.T) {
	fx := newLimitedServer(t, 2)
	req := request{method: http.MethodGet, path: "/v1/outfits", headers: bearer(fx.token)}

	for i := 1; i <= 2; i++ {
		if resp, body := do(t, fx.srv, req); resp.StatusCode != http.StatusOK {
			t.Fatalf("request ke-%d status = %d (body: %v)", i, resp.StatusCode, body)
		}
	}

	resp, body := do(t, fx.srv, req)
	requireStatus(t, resp, body, http.StatusTooManyRequests)
	requireErrorCode(t, body, "rate_limited")

	// Retry-After memberi tahu klien kapan boleh mencoba lagi. Tanpa itu klien
	// hanya bisa menebak, dan tebakan yang salah berarti retry beruntun yang
	// justru memperparah keadaan.
	if got := resp.Header.Get("Retry-After"); got != "7" {
		t.Errorf("Retry-After = %q, ingin \"7\"", got)
	}
}

// Pembatas memakai keyId, bukan alamat IP: semua request dari satu game server
// datang dari IP yang sama, jadi membatasi per IP berarti membatasi seluruh
// pemain di server itu sebagai satu kesatuan.
func TestPembatasMemakaiKeyIDBukanIP(t *testing.T) {
	fx := newLimitedServer(t, 10)

	if resp, body := do(t, fx.srv, request{
		method: http.MethodGet, path: "/v1/outfits", headers: bearer(fx.token),
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (body: %v)", resp.StatusCode, body)
	}

	keyID, err := auth.ParseKeyID(fx.token)
	if err != nil {
		t.Fatalf("ParseKeyID() error = %v", err)
	}
	if got := fx.limiter.key(); got != "key:"+keyID {
		t.Errorf("kunci pembatas = %q, ingin %q", got, "key:"+keyID)
	}
}

// Probe kesehatan dipanggil orchestrator terus-menerus dan tidak boleh ikut
// terbatasi — kalau ikut, pod sehat akan ditandai tidak sehat sendiri.
func TestProbeKesehatanTidakTerbatasi(t *testing.T) {
	fx := newLimitedServer(t, 1)

	for i := 0; i < 5; i++ {
		resp, body := do(t, fx.srv, request{method: http.MethodGet, path: "/healthz"})
		requireStatus(t, resp, body, http.StatusOK)
	}
	if n := fx.limiter.counts["ip:127.0.0.1"] + fx.limiter.counts["key:"]; n != 0 {
		t.Errorf("probe kesehatan ikut dihitung pembatas sebanyak %d kali", n)
	}
}

// Request tanpa kunci ditolak 401 sebelum menyentuh pembatas: membatasi jalur
// yang sudah tertutup hanya menambah biaya.
func TestRequestTanpaKunciTidakMenyentuhPembatas(t *testing.T) {
	fx := newLimitedServer(t, 1)

	for i := 0; i < 3; i++ {
		resp, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/outfits"})
		requireStatus(t, resp, body, http.StatusUnauthorized)
	}
	fx.limiter.mu.Lock()
	defer fx.limiter.mu.Unlock()
	if len(fx.limiter.counts) != 0 {
		t.Errorf("pembatas ikut dipanggil untuk request tanpa kunci: %v", fx.limiter.counts)
	}
}

// RATE_LIMIT_PER_SECOND <= 0 mematikan pembatas sepenuhnya.
func TestPembatasBisaDimatikan(t *testing.T) {
	for _, perSecond := range []int{0, -1} {
		if got := httpapi.NewRateLimiter(perSecond); got != nil {
			t.Errorf("NewRateLimiter(%d) = %v, ingin nil (pembatas mati)", perSecond, got)
		}
	}
	if httpapi.NewRateLimiter(10) == nil {
		t.Error("NewRateLimiter(10) = nil, ingin pembatas aktif")
	}
}
