package httpapi

import (
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

// cashbackHandler memaparkan saldo, redeem, reversal, event, dan rekonsiliasi
// cashback. Semua angka dalam Robux — endpoint ini tidak pernah tahu mata uang.
type cashbackHandler struct {
	cashback *service.Cashback
}

// --- DTO --------------------------------------------------------------------

type cashbackBonusDTO struct {
	Key     string `json:"key"`
	Percent int    `json:"percent"`
	Active  bool   `json:"active"`
}

type cashbackSummaryDTO struct {
	UserID        int64              `json:"userId"`
	Balance       int                `json:"balance"`
	RatePercent   int                `json:"ratePercent"`
	BasePercent   int                `json:"basePercent"`
	MaxPercent    int                `json:"maxPercent"`
	Bonuses       []cashbackBonusDTO `json:"bonuses"`
	MinRedeem     int                `json:"minRedeem"`
	CanRedeem     bool               `json:"canRedeem"`
	PendingRedeem *redeemDTO         `json:"pendingRedeem"`
}

type cashbackEntryDTO struct {
	EntryID     string    `json:"entryId"`
	UserID      int64     `json:"userId"`
	TxID        string    `json:"txId,omitempty"`
	RequestID   string    `json:"requestId,omitempty"`
	Kind        string    `json:"kind"`
	Spend       int       `json:"spend"`
	RatePercent int       `json:"ratePercent"`
	Amount      int       `json:"amount"`
	CreatedAt   time.Time `json:"createdAt"`
}

type redeemDTO struct {
	RequestID   string     `json:"requestId"`
	UserID      int64      `json:"userId"`
	Amount      int        `json:"amount"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requestedAt"`
	ResolvedAt  *time.Time `json:"resolvedAt"`
}

type cashbackEventDTO struct {
	EventID  string    `json:"eventId"`
	Name     string    `json:"name"`
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
	Active   bool      `json:"active"`
	// phase memberi tahu klien apa yang boleh dilakukan pada baris ini tanpa
	// menghitung ulang perbandingan waktu sendiri: upcoming bebas diubah dan
	// dihapus, running hanya endsAt, finished terkunci.
	Phase string `json:"phase"`
	// editable dan deletable adalah kesimpulan dari phase, ditulis eksplisit
	// supaya tombol di UI tidak perlu menerjemahkan aturan backend.
	Editable  bool `json:"editable"`
	Deletable bool `json:"deletable"`
}

func newCashbackEvent(ev model.CashbackEvent, now time.Time) cashbackEventDTO {
	phase := service.PhaseOf(ev, now)
	return cashbackEventDTO{
		EventID:   ev.EventID,
		Name:      ev.Name,
		StartsAt:  ev.StartsAt,
		EndsAt:    ev.EndsAt,
		Active:    ev.Active(now),
		Phase:     string(phase),
		Editable:  phase != service.EventFinished,
		Deletable: phase == service.EventUpcoming,
	}
}

func newCashbackBonuses(bonuses []service.CashbackBonus) []cashbackBonusDTO {
	out := make([]cashbackBonusDTO, 0, len(bonuses))
	for _, b := range bonuses {
		out = append(out, cashbackBonusDTO{Key: b.Key, Percent: b.Percent, Active: b.Active})
	}
	return out
}

func newCashbackEntry(e model.CashbackEntry) cashbackEntryDTO {
	return cashbackEntryDTO{
		EntryID:     e.EntryID,
		UserID:      e.UserID,
		TxID:        e.TxID,
		RequestID:   e.RequestID,
		Kind:        string(e.Kind),
		Spend:       e.Spend,
		RatePercent: e.RatePercent,
		Amount:      e.Amount,
		CreatedAt:   e.CreatedAt,
	}
}

func newRedeem(r model.RedeemRequest) redeemDTO {
	return redeemDTO{
		RequestID:   r.RequestID,
		UserID:      r.UserID,
		Amount:      r.Amount,
		Status:      r.Status,
		RequestedAt: r.RequestedAt,
		ResolvedAt:  r.ResolvedAt,
	}
}

// --- handler ----------------------------------------------------------------

// summary menangani GET /v1/cashback/summary — saldo, rate berjalan beserta
// progres bonus, dan request pending. Inilah satu-satunya yang boleh dirender
// klien: persen dasar, persen bonus publik, dan saldo R$.
func (h *cashbackHandler) summary(w http.ResponseWriter, r *http.Request) {
	userID, err := queryInt64(r, "userId")
	if err != nil {
		writeError(w, err)
		return
	}

	s, err := h.cashback.Summary(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}

	out := cashbackSummaryDTO{
		UserID:      s.UserID,
		Balance:     s.Balance,
		RatePercent: s.Rate.Percent,
		BasePercent: service.BaseRatePercent,
		MaxPercent:  service.MaxRatePercent,
		Bonuses:     newCashbackBonuses(s.Rate.Bonuses),
		MinRedeem:   service.MinRedeem,
		CanRedeem:   s.Balance >= service.MinRedeem && s.PendingRedeem == nil,
	}
	if s.PendingRedeem != nil {
		pending := newRedeem(*s.PendingRedeem)
		out.PendingRedeem = &pending
	}
	writeJSON(w, http.StatusOK, out)
}

type userBalanceDTO struct {
	UserID      int64     `json:"userId"`
	Balance     int       `json:"balance"`
	Accrued     int       `json:"accrued"`
	Redeemed    int       `json:"redeemed"`
	Reversed    int       `json:"reversed"`
	EntryCount  int       `json:"entryCount"`
	LastEntryAt time.Time `json:"lastEntryAt"`
}

// listBalances menangani GET /v1/cashback/balances — ringkasan cashback
// SELURUH pemain, untuk tampilan daftar di dashboard. Pemain tanpa satu pun
// baris ledger tidak muncul: bagi cashback, pemain yang belum pernah
// berbelanja belum punya apa-apa untuk dilaporkan.
func (h *cashbackHandler) listBalances(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}

	page, err := h.cashback.ListBalances(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]userBalanceDTO, 0, len(page.Balances))
	for _, row := range page.Balances {
		out = append(out, userBalanceDTO{
			UserID:      row.UserID,
			Balance:     row.Balance,
			Accrued:     row.Accrued,
			Redeemed:    row.Redeemed,
			Reversed:    row.Reversed,
			EntryCount:  row.EntryCount,
			LastEntryAt: row.LastEntryAt,
		})
	}
	writeJSON(w, http.StatusOK, newListEnvelope(out, page.NextCursor, page.HasMore))
}

// listEntries menangani GET /v1/cashback/entries — ledger append-only per
// pemain, untuk audit dan tampilan riwayat.
func (h *cashbackHandler) listEntries(w http.ResponseWriter, r *http.Request) {
	userID, err := queryInt64(r, "userId")
	if err != nil {
		writeError(w, err)
		return
	}
	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}

	page, err := h.cashback.ListEntries(r.Context(), userID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, err)
		return
	}

	entries := make([]cashbackEntryDTO, 0, len(page.Entries))
	for _, e := range page.Entries {
		entries = append(entries, newCashbackEntry(e))
	}
	writeJSON(w, http.StatusOK, newListEnvelope(entries, page.NextCursor, page.HasMore))
}

type createRedeemBody struct {
	UserID int64 `json:"userId"`
}

// createRedeem menangani POST /v1/cashback/redeems. Seluruh saldo dipotong dan
// dijadikan satu request pending; fulfillment dikerjakan tim internal di luar
// sistem ini.
func (h *cashbackHandler) createRedeem(w http.ResponseWriter, r *http.Request) {
	var body createRedeemBody
	if !decodeJSON(w, r, &body) {
		return
	}

	request, err := h.cashback.Redeem(r.Context(), body.UserID)
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Location", "/v1/cashback/redeems/"+request.RequestID)
	writeJSON(w, http.StatusCreated, newRedeem(request))
}

// listRedeems menangani GET /v1/cashback/redeems. Tanpa userId daftar berisi
// semua pemain — dipakai tim internal mengambil antrean ?status=pending.
func (h *cashbackHandler) listRedeems(w http.ResponseWriter, r *http.Request) {
	userID, err := queryInt64(r, "userId")
	if err != nil {
		writeError(w, err)
		return
	}
	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}

	page, err := h.cashback.ListRedeems(r.Context(), userID,
		r.URL.Query().Get("status"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, err)
		return
	}

	redeems := make([]redeemDTO, 0, len(page.Redeems))
	for _, req := range page.Redeems {
		redeems = append(redeems, newRedeem(req))
	}
	writeJSON(w, http.StatusOK, newListEnvelope(redeems, page.NextCursor, page.HasMore))
}

type resolveRedeemBody struct {
	Status string `json:"status"`
}

// resolveRedeem menangani PATCH /v1/cashback/redeems/{requestId} — completed
// setelah fulfillment selesai, rejected untuk membatalkan dan mengembalikan
// saldo. Idempoten untuk status yang sama.
func (h *cashbackHandler) resolveRedeem(w http.ResponseWriter, r *http.Request) {
	var body resolveRedeemBody
	if !decodeJSON(w, r, &body) {
		return
	}

	request, err := h.cashback.ResolveRedeem(r.Context(), r.PathValue("requestId"), body.Status)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newRedeem(request))
}

type createReversalBody struct {
	TxID string `json:"txId"`
}

// createReversal menangani POST /v1/cashback/reversals — refund/chargeback
// menarik kembali cashback transaksi terkait. Saldo boleh menjadi minus.
func (h *cashbackHandler) createReversal(w http.ResponseWriter, r *http.Request) {
	var body createReversalBody
	if !decodeJSON(w, r, &body) {
		return
	}

	entry, replay, err := h.cashback.Reverse(r.Context(), body.TxID)
	if err != nil {
		writeError(w, err)
		return
	}

	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	writeJSON(w, status, newCashbackEntry(entry))
}

type createEventBody struct {
	Name     string    `json:"name"`
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
}

// createEvent menangani POST /v1/cashback/events — menjadwalkan jendela bonus
// event mingguan.
func (h *cashbackHandler) createEvent(w http.ResponseWriter, r *http.Request) {
	var body createEventBody
	if !decodeJSON(w, r, &body) {
		return
	}

	ev, err := h.cashback.CreateEvent(r.Context(), service.CreateEventInput{
		Name:     body.Name,
		StartsAt: body.StartsAt,
		EndsAt:   body.EndsAt,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", "/v1/cashback/events/"+ev.EventID)
	writeJSON(w, http.StatusCreated, newCashbackEvent(ev, time.Now().UTC()))
}

type updateEventBody struct {
	// Semua opsional; nil berarti tidak diubah.
	Name     *string    `json:"name"`
	StartsAt *time.Time `json:"startsAt"`
	EndsAt   *time.Time `json:"endsAt"`
}

// updateEvent menangani PATCH /v1/cashback/events/{eventId}.
func (h *cashbackHandler) updateEvent(w http.ResponseWriter, r *http.Request) {
	var body updateEventBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == nil && body.StartsAt == nil && body.EndsAt == nil {
		writeError(w, apierr.Unprocessable("empty_update", "Tidak ada yang diubah"))
		return
	}

	ev, err := h.cashback.UpdateEvent(r.Context(), r.PathValue("eventId"), service.UpdateEventInput{
		Name:     body.Name,
		StartsAt: body.StartsAt,
		EndsAt:   body.EndsAt,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newCashbackEvent(ev, time.Now().UTC()))
}

// removeEvent menangani DELETE /v1/cashback/events/{eventId} — hanya untuk
// event yang belum mulai.
func (h *cashbackHandler) removeEvent(w http.ResponseWriter, r *http.Request) {
	if err := h.cashback.DeleteEvent(r.Context(), r.PathValue("eventId")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listEvents menangani GET /v1/cashback/events — event aktif dan mendatang.
func (h *cashbackHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	events, err := h.cashback.ListEvents(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}

	now := time.Now().UTC()
	out := make([]cashbackEventDTO, 0, len(events))
	for _, ev := range events {
		out = append(out, newCashbackEvent(ev, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// reconciliation menangani GET /v1/cashback/reconciliation — agregat ledger
// untuk rekonsiliasi periodik tim internal. Rentang kosong = bulan berjalan.
func (h *cashbackHandler) reconciliation(w http.ResponseWriter, r *http.Request) {
	from, err := queryTime(r, "from")
	if err != nil {
		writeError(w, err)
		return
	}
	to, err := queryTime(r, "to")
	if err != nil {
		writeError(w, err)
		return
	}

	report, err := h.cashback.Reconcile(r.Context(), from, to)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": report.From,
		"to":   report.To,
		"totals": map[string]int{
			"accrued":     report.Totals.Accrued,
			"reversed":    report.Totals.Reversed,
			"redeemed":    report.Totals.Redeemed,
			"returned":    report.Totals.Returned,
			"outstanding": report.Totals.Outstanding,
		},
	})
}
