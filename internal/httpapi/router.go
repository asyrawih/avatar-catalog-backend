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

	mux := http.NewServeMux()

	// Probe kesehatan sengaja di luar /v1 dan tanpa autentikasi.
	// healthz cukup membuktikan proses hidup; readyz ikut memeriksa dependensi.
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /readyz", handleReady(deps.Readiness))

	// Outfit.
	mux.HandleFunc("GET /v1/outfits", outfits.list)
	mux.Handle("POST /v1/outfits", chain(http.HandlerFunc(outfits.create),
		idempotent(deps.Idempotency, "outfits.create", false)))
	mux.HandleFunc("POST /v1/outfits/resolve", outfits.resolve)
	mux.HandleFunc("GET /v1/outfits/{outfitId}", outfits.get)
	mux.HandleFunc("PATCH /v1/outfits/{outfitId}", outfits.update)
	mux.HandleFunc("DELETE /v1/outfits/{outfitId}", outfits.remove)
	mux.HandleFunc("PUT /v1/outfits/{outfitId}/items", outfits.replaceItems)

	// Rig. Rig di-upload ke Roblox lebih dulu; endpoint ini hanya registry-nya.
	mux.HandleFunc("GET /v1/templates", templates.list)
	mux.HandleFunc("POST /v1/templates", templates.register)
	mux.HandleFunc("GET /v1/templates/{templateId}", templates.get)
	mux.HandleFunc("PATCH /v1/templates/{templateId}", templates.update)

	// Transaksi. Idempotensinya ditegakkan service lewat kolom unik, jadi
	// endpoint ini tidak dibungkus middleware idempotent.
	mux.HandleFunc("POST /v1/transactions", transactions.create)
	mux.HandleFunc("GET /v1/transactions", transactions.list)

	// Cashback. Accrual terjadi otomatis di POST /v1/transactions; endpoint di
	// sini melayani tampilan klien (summary), redeem, dan operasi tim internal
	// (resolusi redeem, reversal, jadwal event, rekonsiliasi). Idempotensi
	// redeem/reversal ditegakkan batasan unik di database, bukan middleware.
	if deps.Cashback != nil {
		cashback := &cashbackHandler{cashback: deps.Cashback}
		mux.HandleFunc("GET /v1/cashback/summary", cashback.summary)
		mux.HandleFunc("GET /v1/cashback/entries", cashback.listEntries)
		mux.HandleFunc("POST /v1/cashback/redeems", cashback.createRedeem)
		mux.HandleFunc("GET /v1/cashback/redeems", cashback.listRedeems)
		mux.HandleFunc("PATCH /v1/cashback/redeems/{requestId}", cashback.resolveRedeem)
		mux.HandleFunc("POST /v1/cashback/reversals", cashback.createReversal)
		mux.HandleFunc("GET /v1/cashback/events", cashback.listEvents)
		mux.HandleFunc("POST /v1/cashback/events", cashback.createEvent)
		mux.HandleFunc("GET /v1/cashback/reconciliation", cashback.reconciliation)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, apierr.NotFound("route_not_found", "Rute tidak ditemukan"))
	})

	return chain(mux,
		requestID,
		recoverPanic(deps.Logger),
		accessLog(deps.Logger),
		authenticate(deps.Auth),
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
