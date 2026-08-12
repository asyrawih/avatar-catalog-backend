// Package httpapi memaparkan outfit avatar lewat HTTP.
//
// Konvensi yang berlaku di seluruh endpoint:
//   - POST yang membuat baris memakai header Idempotency-Key
//   - PUT/PATCH/DELETE idempoten dari desainnya
//   - paginasi memakai cursor, bukan offset
//   - detail item (name, assetType, price) datang dari klien dan disimpan apa
//     adanya; backend tidak memverifikasinya ke Roblox
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/idempotency"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

// Deps adalah semua yang dibutuhkan router.
type Deps struct {
	Outfits      *service.Outfits
	Transactions *service.Transactions
	Cashback     *service.Cashback
	Templates    *service.Templates
	Idempotency  idempotency.Store
	Auth         Authenticator
	Logger       *slog.Logger

	// RateLimit membatasi request per kunci API. nil berarti tanpa pembatas.
	RateLimit Limiter

	// Readiness adalah pemeriksaan dependensi luar (Postgres, Redis) yang
	// dijalankan /readyz. Kosong berarti server selalu dianggap siap.
	Readiness map[string]func(context.Context) error
}

// NewRouter merangkai seluruh rute API.
func NewRouter(deps Deps) http.Handler {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Auth == nil {
		deps.Auth = UnverifiedActorAuth{}
	}
	if deps.Idempotency == nil {
		deps.Idempotency = idempotency.NewMemoryStore(idempotency.DefaultTTL)
	}

	outfits := &outfitHandler{outfits: deps.Outfits}
	transactions := &transactionHandler{transactions: deps.Transactions}
	templates := &templateHandler{templates: deps.Templates}

	// Dua mux, dan pemisahannya penting: yang satu diautentikasi, yang satu
	// tidak.
	//
	// Probe kesehatan HARUS tetap bisa dipanggil tanpa kunci. Orchestrator
	// (kubelet, healthcheck compose, load balancer) tidak memegang kredensial,
	// jadi probe yang butuh kunci akan menandai pod sehat sebagai tidak sehat —
	// dan sekali kunci dirotasi, seluruh deployment mati sendiri.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /readyz", handleReady(deps.Readiness))

	// Semuanya di bawah ini butuh kunci. Urutannya: autentikasi dulu, baru
	// pembatas — pembatas memakai keyId sebagai kunci hitungannya, jadi ia
	// harus tahu siapa pemanggilnya. Probe kesehatan di atas tidak ikut
	// terbatasi; orchestrator memanggilnya terus-menerus dan itu memang wajar.
	api := http.NewServeMux()
	mux.Handle("/", chain(api, authenticate(deps.Auth), rateLimit(deps.RateLimit)))

	// Tiap rute /v1 dibungkus scope yang dibutuhkannya. Ditulis di sini, bukan
	// diperiksa di dalam handler: rute baru yang lupa diberi scope langsung
	// terlihat saat membaca daftar ini, sedangkan pemeriksaan yang lupa ditulis
	// di dalam handler tidak kelihatan dari mana pun.
	//
	// Pola penulisannya: get(mux, pola, handler, scope).
	read := auth.ScopeCatalogRead
	write := auth.ScopeCatalogWrite

	// Outfit.
	route(api, "GET /v1/outfits", outfits.list, read)
	route(api, "POST /v1/outfits", outfits.create, write,
		idempotent(deps.Idempotency, "outfits.create", false))
	route(api, "POST /v1/outfits/resolve", outfits.resolve, read)
	// Terdaftar sebelum {outfitId} secara makna: mux Go 1.22 memilih pola
	// literal "search" di atas wildcard, jadi tidak bentrok.
	route(api, "GET /v1/outfits/search", outfits.search, read)
	route(api, "GET /v1/outfits/{outfitId}", outfits.get, read)
	route(api, "PATCH /v1/outfits/{outfitId}", outfits.update, write)
	route(api, "DELETE /v1/outfits/{outfitId}", outfits.remove, write)
	route(api, "PUT /v1/outfits/{outfitId}/items", outfits.replaceItems, write)

	// Like dan view. Keduanya idempoten dari desainnya — like kedua dari
	// pemain yang sama tidak menambah apa pun, dan view memang mencatat tiap
	// kejadian — jadi tidak dibungkus middleware idempotent.
	route(api, "POST /v1/outfits/{outfitId}/likes", outfits.like, write)
	route(api, "DELETE /v1/outfits/{outfitId}/likes", outfits.unlike, write)
	route(api, "POST /v1/outfits/{outfitId}/views", outfits.recordView, write)

	// Rig. Rig di-upload ke Roblox lebih dulu; endpoint ini hanya registry-nya.
	route(api, "GET /v1/templates", templates.list, read)
	route(api, "POST /v1/templates", templates.register, write)
	route(api, "GET /v1/templates/{templateId}", templates.get, read)
	route(api, "PATCH /v1/templates/{templateId}", templates.update, write)

	// Transaksi. Idempotensinya ditegakkan service lewat kolom unik, jadi
	// endpoint ini tidak dibungkus middleware idempotent.
	route(api, "POST /v1/transactions", transactions.create, auth.ScopeTransactionsWrite)
	route(api, "GET /v1/transactions", transactions.list, auth.ScopeTransactionsRead)

	// Cashback. Accrual terjadi otomatis di POST /v1/transactions; endpoint di
	// sini melayani tampilan klien (summary), redeem, dan operasi tim internal
	// (resolusi redeem, reversal, jadwal event, rekonsiliasi). Idempotensi
	// redeem/reversal ditegakkan batasan unik di database, bukan middleware.
	//
	// Perhatikan pembagian scope-nya: membaca saldo, membuat request redeem,
	// dan MENUNTASKAN redeem adalah tiga izin berbeda. Game server boleh dua
	// yang pertama; yang ketiga jalur uang keluar dan hanya untuk kunci
	// dashboard.
	if deps.Cashback != nil {
		cashback := &cashbackHandler{cashback: deps.Cashback}
		route(api, "GET /v1/cashback/summary", cashback.summary, auth.ScopeCashbackRead)
		route(api, "GET /v1/cashback/entries", cashback.listEntries, auth.ScopeCashbackRead)
		route(api, "POST /v1/cashback/redeems", cashback.createRedeem, auth.ScopeCashbackRedeem)
		route(api, "GET /v1/cashback/redeems", cashback.listRedeems, auth.ScopeCashbackRead)
		route(api, "PATCH /v1/cashback/redeems/{requestId}", cashback.resolveRedeem, auth.ScopeCashbackAdmin)
		route(api, "POST /v1/cashback/reversals", cashback.createReversal, auth.ScopeCashbackAdmin)
		route(api, "GET /v1/cashback/events", cashback.listEvents, auth.ScopeCashbackRead)
		route(api, "POST /v1/cashback/events", cashback.createEvent, auth.ScopeCashbackAdmin)
		route(api, "GET /v1/cashback/reconciliation", cashback.reconciliation, auth.ScopeCashbackAdmin)
	}

	api.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, apierr.NotFound("route_not_found", "Rute tidak ditemukan"))
	})

	return chain(mux,
		requestID,
		recoverPanic(deps.Logger),
		accessLog(deps.Logger),
	)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readinessTimeout menjaga probe tetap cepat gagal saat dependensi menggantung.
const readinessTimeout = 2 * time.Second

// handleReady menjalankan seluruh pemeriksaan dependensi dan melaporkan
// masing-masing hasilnya, supaya orchestrator tahu bagian mana yang bermasalah.
func handleReady(checks map[string]func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		results := make(map[string]string, len(checks))
		ready := true
		for name, check := range checks {
			if err := check(ctx); err != nil {
				results[name] = "error: " + err.Error()
				ready = false
				continue
			}
			results[name] = "ok"
		}

		status := http.StatusOK
		state := "ok"
		if !ready {
			status = http.StatusServiceUnavailable
			state = "degraded"
		}
		writeJSON(w, status, map[string]any{"status": state, "checks": results})
	}
}

// route mendaftarkan satu rute beserta scope yang dibutuhkannya, plus
// middleware tambahan bila ada.
func route(mux *http.ServeMux, pattern string, h http.HandlerFunc, scope auth.Scope, extra ...func(http.Handler) http.Handler) {
	mux.Handle(pattern, chain(http.Handler(h), append([]func(http.Handler) http.Handler{requireScope(scope)}, extra...)...))
}
