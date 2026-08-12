package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/httpapi"
	"github.com/hanan/avatar-catalog-backend/internal/idempotency"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// authFixture adalah server dengan autentikasi kunci aktif, plus kunci siap
// pakai untuk tiap role.
type authFixture struct {
	srv    *httptest.Server
	keys   *store.MemoryAPIKeys
	tokens map[string]string // role -> token utuh
}

func newAuthServer(t *testing.T) *authFixture {
	t.Helper()

	outfits := store.NewMemoryOutfits()
	templates := store.NewMemoryTemplates()
	transactions := store.NewMemoryTransactions()
	store.SeedData(templates, outfits)

	keys := store.NewMemoryAPIKeys()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cashback := service.NewCashback(store.NewMemoryCashback(transactions))
	handler := httpapi.NewRouter(httpapi.Deps{
		Outfits:      service.NewOutfits(outfits, templates),
		Transactions: service.NewTransactions(transactions, cashback),
		Cashback:     cashback,
		Templates:    service.NewTemplates(templates),
		Idempotency:  idempotency.NewMemoryStore(time.Hour),
		Auth:         httpapi.NewKeyAuth(keys, logger),
		Logger:       logger,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	fx := &authFixture{srv: srv, keys: keys, tokens: map[string]string{}}
	for role := range auth.Roles {
		fx.tokens[role] = fx.issue(t, role, auth.Roles[role], nil)
	}
	return fx
}

// issue menerbitkan kunci lalu mengembalikan token utuhnya.
func (f *authFixture) issue(t *testing.T, name string, scopes []auth.Scope, expiresAt *time.Time) string {
	t.Helper()

	token, err := auth.Generate(auth.EnvTest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	err = f.keys.Create(context.Background(), auth.Key{
		KeyID:     token.KeyID,
		Hash:      token.Hash,
		Name:      name,
		Scopes:    scopes,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return token.Secret
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func withUser(token, userID string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token, "X-User-Id": userID}
}

func TestTanpaTokenDitolak(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/outfits"})

	requireStatus(t, resp, body, http.StatusUnauthorized)
	requireErrorCode(t, body, "unauthenticated")
}

// Semua kegagalan autentikasi harus menjawab persis sama. Membedakan "bentuk
// salah" dari "tidak dikenal" memberi tahu penyerang seberapa jauh tebakannya
// sudah benar.
func TestSemuaKegagalanTokenMenjawabSama(t *testing.T) {
	fx := newAuthServer(t)
	valid := fx.tokens["game-server"]

	dicabut := fx.issue(t, "dicabut", auth.Roles["game-server"], nil)
	keyID, err := auth.ParseKeyID(dicabut)
	if err != nil {
		t.Fatalf("ParseKeyID() error = %v", err)
	}
	if err := fx.keys.Revoke(context.Background(), keyID, time.Now().UTC()); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	lalu := time.Now().UTC().Add(-time.Hour)
	kedaluwarsa := fx.issue(t, "kedaluwarsa", auth.Roles["game-server"], &lalu)

	cases := map[string]map[string]string{
		"bentuk ngawur":     bearer("bukan-token-sama-sekali"),
		"keyId tidak ada":   bearer("acb_test_tidakadakunciini_" + valid[len(valid)-52:]),
		"rahasia salah":     bearer(valid[:len(valid)-4] + "aaaa"),
		"kunci dicabut":     bearer(dicabut),
		"kunci kedaluwarsa": bearer(kedaluwarsa),
		"skema salah":       {"Authorization": "Basic " + valid},
	}
	for name, headers := range cases {
		resp, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/outfits", headers: headers})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, ingin 401 (body: %v)", name, resp.StatusCode, body)
			continue
		}
		requireErrorCode(t, body, "unauthenticated")
	}
}

func TestTokenSahDiterima(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/outfits",
		headers: bearer(fx.tokens["game-server"]),
	})

	requireStatus(t, resp, body, http.StatusOK)
}

// Nama skema tidak peduli huruf besar-kecil menurut RFC 7235.
func TestSkemaBearerTidakPeduliHurufBesarKecil(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/outfits",
		headers: map[string]string{"Authorization": "bearer " + fx.tokens["game-server"]},
	})

	requireStatus(t, resp, body, http.StatusOK)
}

// Ini pembagian scope yang paling penting: kunci game server tidak boleh
// menyentuh jalur uang keluar.
func TestGameServerTidakBisaMenuntaskanRedeem(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPatch,
		path:    "/v1/cashback/redeems/req_apa_saja",
		body:    map[string]any{"status": "completed"},
		headers: bearer(fx.tokens["game-server"]),
	})

	requireStatus(t, resp, body, http.StatusForbidden)
	requireErrorCode(t, body, "insufficient_scope")
}

func TestDashboardBisaMenuntaskanRedeem(t *testing.T) {
	fx := newAuthServer(t)

	// Bukan 403: request lolos scope, lalu ditolak karena requestId-nya memang
	// tidak ada. Itu yang membuktikan scope-nya lulus.
	resp, body := do(t, fx.srv, request{
		method:  http.MethodPatch,
		path:    "/v1/cashback/redeems/req_tidak_ada",
		body:    map[string]any{"status": "completed"},
		headers: bearer(fx.tokens["dashboard"]),
	})

	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("dashboard ditolak scope-nya: %v", body)
	}
}

func TestKunciAITidakBisaMenulis(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits",
		body:    createOutfitBody(),
		headers: bearer(fx.tokens["ai"]),
	})

	requireStatus(t, resp, body, http.StatusForbidden)
	requireErrorCode(t, body, "insufficient_scope")
}

func TestKunciAIBisaMembaca(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/outfits",
		headers: bearer(fx.tokens["ai"]),
	})

	requireStatus(t, resp, body, http.StatusOK)
}

func TestPublicReadTidakBisaMembacaCashback(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/cashback/summary?userId=" + seedUser,
		headers: bearer(fx.tokens["public-read"]),
	})

	requireStatus(t, resp, body, http.StatusForbidden)
	requireErrorCode(t, body, "insufficient_scope")
}

// Menyamar jadi pemain adalah kemampuan tersendiri: kunci dashboard yang bocor
// tetap tidak bisa menyukai atau menukar cashback atas nama siapa pun.
func TestKunciTanpaActorAssertTidakBisaMenyamarJadiPemain(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/outfits",
		headers: withUser(fx.tokens["dashboard"], seedUser),
	})

	requireStatus(t, resp, body, http.StatusForbidden)
	requireErrorCode(t, body, "actor_assert_forbidden")
}

func TestGameServerBisaMenyamarJadiPemain(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits/" + seedOutfit + "/likes",
		headers: withUser(fx.tokens["game-server"], seedUser),
	})

	requireStatus(t, resp, body, http.StatusOK)
	if body["likeCount"] != float64(1) {
		t.Errorf("likeCount = %v, ingin 1", body["likeCount"])
	}
}

// Tanpa X-User-Id, like tidak tahu siapa yang menyukai — ditolak service,
// bukan diam-diam dicatat atas nama entah siapa.
func TestLikeTanpaHeaderPemainDitolak(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits/" + seedOutfit + "/likes",
		headers: bearer(fx.tokens["game-server"]),
	})

	requireStatus(t, resp, body, http.StatusUnauthorized)
	requireErrorCode(t, body, "actor_required")
}

func TestHeaderPemainNgawurDitolak(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/outfits",
		headers: withUser(fx.tokens["game-server"], "bukan-angka"),
	})

	requireStatus(t, resp, body, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_actor")
}

// Probe kesehatan harus tetap bisa dipanggil tanpa kunci: orchestrator tidak
// memegang kredensial, dan probe yang butuh kunci akan menandai pod sehat jadi
// tidak sehat begitu kuncinya dirotasi.
func TestProbeKesehatanTidakButuhKunci(t *testing.T) {
	fx := newAuthServer(t)

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, body := do(t, fx.srv, request{method: http.MethodGet, path: path})
		requireStatus(t, resp, body, http.StatusOK)
	}
}

// Kunci yang dipakai harus tercatat, supaya saat ada yang aneh bisa ditelusuri
// kunci milik siapa — dan kunci yang tidak pernah dipakai bisa dicabut.
func TestPemakaianKunciTercatat(t *testing.T) {
	fx := newAuthServer(t)
	token := fx.tokens["ai"]

	keyID, err := auth.ParseKeyID(token)
	if err != nil {
		t.Fatalf("ParseKeyID() error = %v", err)
	}
	before, err := fx.keys.ByKeyID(context.Background(), keyID)
	if err != nil {
		t.Fatalf("ByKeyID() error = %v", err)
	}
	if before.LastUsedAt != nil {
		t.Fatal("lastUsedAt sudah terisi sebelum kunci dipakai")
	}

	if resp, body := do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/outfits",
		headers: bearer(token),
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (body: %v)", resp.StatusCode, body)
	}

	after, err := fx.keys.ByKeyID(context.Background(), keyID)
	if err != nil {
		t.Fatalf("ByKeyID() error = %v", err)
	}
	if after.LastUsedAt == nil {
		t.Error("lastUsedAt masih kosong setelah kunci dipakai")
	}
}

// Pencabutan harus berlaku pada request berikutnya, tanpa restart.
func TestPencabutanBerlakuSeketika(t *testing.T) {
	fx := newAuthServer(t)
	token := fx.issue(t, "sementara", auth.Roles["ai"], nil)

	if resp, body := do(t, fx.srv, request{
		method: http.MethodGet, path: "/v1/outfits", headers: bearer(token),
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sebelum dicabut status = %d (body: %v)", resp.StatusCode, body)
	}

	keyID, _ := auth.ParseKeyID(token)
	if err := fx.keys.Revoke(context.Background(), keyID, time.Now().UTC()); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	resp, body := do(t, fx.srv, request{
		method: http.MethodGet, path: "/v1/outfits", headers: bearer(token),
	})
	requireStatus(t, resp, body, http.StatusUnauthorized)
}
