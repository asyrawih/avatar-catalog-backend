package service

import (
	"context"
	"errors"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Parameter cashback. Semua persentase dalam bilangan bulat persen dan semua
// jumlah dalam Robux — tidak ada konversi mata uang di sistem ini.
const (
	// BaseRatePercent berlaku untuk semua pemain di semua transaksi.
	BaseRatePercent = 20
	// MaxRatePercent adalah hard cap — dasar klaim "cashback up to 40%".
	MaxRatePercent = 40

	// EventBonusPercent aktif saat jendela CASHBACK_EVENT sedang berjalan.
	EventBonusPercent = 10
	// StreakBonusPercent aktif saat pemain belanja >= streakDays hari beruntun.
	StreakBonusPercent = 5
	// FirstBonusPercent aktif pada transaksi berbayar pertama seumur akun.
	FirstBonusPercent = 10
	// LoyalBonusPercent aktif permanen setelah total belanja mencapai ambang.
	LoyalBonusPercent = 5

	// LoyalSpendThreshold adalah total spend kumulatif (Robux) untuk bonus loyal.
	LoyalSpendThreshold = 5000
	// MinRedeem adalah saldo minimum agar request redeem bisa dibuat.
	MinRedeem = 100

	// streakDays adalah panjang runtutan hari belanja yang mengaktifkan bonus,
	// termasuk hari transaksi yang sedang dinilai.
	streakDays = 3
)

// CashbackBonus adalah satu jalur bonus beserta status aktifnya, untuk
// ditampilkan sebagai progres "boost sampai 40%" di klien.
type CashbackBonus struct {
	Key     string
	Percent int
	Active  bool
}

// CashbackRate adalah hasil penentuan rate untuk satu pemain pada satu waktu.
// Percent sudah dijumlahkan dari dasar plus bonus aktif dan dipotong cap.
type CashbackRate struct {
	Percent int
	Bonuses []CashbackBonus
}

// CashbackSummary adalah muatan GET /v1/cashback/summary.
type CashbackSummary struct {
	UserID        int64
	Balance       int
	Rate          CashbackRate
	PendingRedeem *model.RedeemRequest // nil bila tidak ada
}

// CashbackEntryPage adalah satu halaman ledger cashback.
type CashbackEntryPage struct {
	Entries    []model.CashbackEntry
	NextCursor string
	HasMore    bool
}

// RedeemPage adalah satu halaman daftar request redeem.
type RedeemPage struct {
	Redeems    []model.RedeemRequest
	NextCursor string
	HasMore    bool
}

// ReconciliationReport adalah agregat ledger untuk rekonsiliasi periodik.
// Semua angka Robux; konversi ke kewajiban internal dikerjakan di luar sistem.
type ReconciliationReport struct {
	From   time.Time
	To     time.Time
	Totals model.CashbackTotals
}

// Cashback menghitung, mencatat, dan mencairkan cashback Robux.
//
// Sumber kebenaran spend adalah TRANSACTION + TRANSACTION_ITEM; ledger
// CASHBACK_ENTRY hanya menyimpan hasil hitung supaya rate yang berlaku saat
// transaksi terjadi tidak hilang saat jadwal event atau riwayat streak berubah.
type Cashback struct {
	cashback store.Cashback
	now      func() time.Time
	newID    func(prefix string) string
}

// NewCashback merangkai service cashback.
func NewCashback(cashback store.Cashback) *Cashback {
	return &Cashback{
		cashback: cashback,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    func(prefix string) string { return prefix + "_" + randomHex(4) },
	}
}

// RateFor menentukan rate efektif pemain pada waktu at.
//
// excludeTxID membuang transaksi yang sedang di-accrue dari statistik, supaya
// bonus first dan loyal dinilai dari riwayat SEBELUM transaksi itu; kosongkan
// untuk pratinjau rate "kalau belanja sekarang".
func (s *Cashback) RateFor(ctx context.Context, userID int64, at time.Time, excludeTxID string) (CashbackRate, error) {
	_, eventActive, err := s.cashback.ActiveEvent(ctx, at)
	if err != nil {
		return CashbackRate{}, err
	}

	// Streak butuh hari belanja pada streakDays-1 hari sebelum at; hari at
	// sendiri dihitung dari transaksi yang sedang dinilai.
	day := at.UTC().Truncate(24 * time.Hour)
	stats, err := s.cashback.SpendStats(ctx, userID, excludeTxID, day.AddDate(0, 0, -(streakDays-1)))
	if err != nil {
		return CashbackRate{}, err
	}

	streak := true
	for i := 1; i < streakDays; i++ {
		if !stats.SpendDays[day.AddDate(0, 0, -i).Format("2006-01-02")] {
			streak = false
			break
		}
	}

	bonuses := []CashbackBonus{
		{Key: "event", Percent: EventBonusPercent, Active: eventActive},
		{Key: "streak", Percent: StreakBonusPercent, Active: streak},
		{Key: "first", Percent: FirstBonusPercent, Active: stats.PurchaseCount == 0},
		{Key: "loyal", Percent: LoyalBonusPercent, Active: stats.LifetimeSpend >= LoyalSpendThreshold},
	}

	percent := BaseRatePercent
	for _, b := range bonuses {
		if b.Active {
			percent += b.Percent
		}
	}
	if percent > MaxRatePercent {
		percent = MaxRatePercent
	}
	return CashbackRate{Percent: percent, Bonuses: bonuses}, nil
}

// Accrue mencatat cashback untuk satu transaksi yang baru tercatat.
//
// Idempoten lewat batasan unik (txId, kind) di ledger: retry POST transaksi
// yang memanggil ulang Accrue tidak menggandakan cashback. Transaksi tanpa
// spend sukses tidak menghasilkan baris apa pun.
func (s *Cashback) Accrue(ctx context.Context, tx model.Transaction) error {
	spend := tx.RobuxTotal()
	if spend <= 0 {
		return nil
	}

	rate, err := s.RateFor(ctx, tx.UserID, tx.OccurredAt, tx.TxID)
	if err != nil {
		return err
	}

	err = s.cashback.CreateEntry(ctx, model.CashbackEntry{
		EntryID:     s.newID("cbe"),
		UserID:      tx.UserID,
		TxID:        tx.TxID,
		Kind:        model.CashbackAccrual,
		Spend:       spend,
		RatePercent: rate.Percent,
		// Pembagian bulat ke bawah — padanan math.floor pada spec.
		Amount:    spend * rate.Percent / 100,
		CreatedAt: s.now(),
	})
	if errors.Is(err, store.ErrDuplicateEntry) {
		return nil // replay: cashback transaksi ini sudah tercatat
	}
	return err
}

// Summary mengembalikan saldo, rate berjalan, dan request pending pemain.
func (s *Cashback) Summary(ctx context.Context, userID int64) (CashbackSummary, error) {
	if userID <= 0 {
		return CashbackSummary{}, apierr.BadRequest("missing_user_id", "Parameter userId wajib diisi")
	}

	balance, err := s.cashback.Balance(ctx, userID)
	if err != nil {
		return CashbackSummary{}, err
	}
	rate, err := s.RateFor(ctx, userID, s.now(), "")
	if err != nil {
		return CashbackSummary{}, err
	}

	summary := CashbackSummary{UserID: userID, Balance: balance, Rate: rate}
	pending, _, err := s.cashback.ListRedeems(ctx, store.RedeemFilter{UserID: userID, Status: model.RedeemPending}, nil, 1)
	if err != nil {
		return CashbackSummary{}, err
	}
	if len(pending) > 0 {
		summary.PendingRedeem = &pending[0]
	}
	return summary, nil
}

// ListEntries mengembalikan ledger cashback pemain, terbaru dulu.
func (s *Cashback) ListEntries(ctx context.Context, userID int64, rawCursor string, limit int) (CashbackEntryPage, error) {
	if userID <= 0 {
		return CashbackEntryPage{}, apierr.BadRequest("missing_user_id", "Parameter userId wajib diisi")
	}

	after, err := decodeCursor(rawCursor)
	if err != nil {
		return CashbackEntryPage{}, err
	}

	limit = paging.ClampLimit(limit, DefaultPageSize, MaxPageSize)
	rows, hasMore, err := s.cashback.ListEntries(ctx, userID, after, limit)
	if err != nil {
		return CashbackEntryPage{}, err
	}

	page := CashbackEntryPage{Entries: rows, HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = paging.Encode(paging.KeysetCursor{At: last.CreatedAt, ID: last.EntryID})
		if err != nil {
			return CashbackEntryPage{}, err
		}
	}
	return page, nil
}

// Redeem membuat request pencairan untuk seluruh saldo pemain saat ini.
//
// Saldo dipotong saat request dibuat, jadi tidak ada jendela saldo yang sama
// dicairkan dua kali; kalau request ditolak, potongan dikembalikan lewat
// ResolveRedeem. Maksimal satu request pending per pemain — ditolak store bila
// sudah ada.
func (s *Cashback) Redeem(ctx context.Context, userID int64) (model.RedeemRequest, error) {
	if userID <= 0 {
		return model.RedeemRequest{}, apierr.Unprocessable("missing_user_id", "Field userId wajib diisi")
	}

	balance, err := s.cashback.Balance(ctx, userID)
	if err != nil {
		return model.RedeemRequest{}, err
	}
	if balance < MinRedeem {
		return model.RedeemRequest{}, apierr.Unprocessable("balance_below_minimum", "Saldo cashback belum mencapai minimum redeem").
			WithDetails(map[string]any{"balance": balance, "minRedeem": MinRedeem})
	}

	now := s.now()
	request := model.RedeemRequest{
		RequestID:   s.newID("rdm"),
		UserID:      userID,
		Amount:      balance,
		Status:      model.RedeemPending,
		RequestedAt: now,
	}
	deduct := model.CashbackEntry{
		EntryID:   s.newID("cbe"),
		UserID:    userID,
		RequestID: request.RequestID,
		Kind:      model.CashbackRedeem,
		Amount:    -balance,
		CreatedAt: now,
	}

	if err := s.cashback.CreateRedeem(ctx, request, deduct); err != nil {
		if errors.Is(err, store.ErrPendingRedeemExists) {
			return model.RedeemRequest{}, apierr.Conflict("redeem_pending_exists", "Masih ada request redeem yang belum selesai")
		}
		return model.RedeemRequest{}, err
	}
	return request, nil
}

// ResolveRedeem menuntaskan request pending: completed setelah fulfillment
// internal selesai, atau rejected — yang mengembalikan potongan saldo.
//
// Mengulang resolusi dengan status yang sama adalah no-op yang mengembalikan
// request apa adanya; mengubah request yang sudah tuntas ke status lain ditolak.
func (s *Cashback) ResolveRedeem(ctx context.Context, requestID, status string) (model.RedeemRequest, error) {
	if status != model.RedeemCompleted && status != model.RedeemRejected {
		return model.RedeemRequest{}, apierr.Unprocessable("invalid_status", "status harus completed atau rejected")
	}

	now := s.now()
	resolved, err := s.cashback.ResolveRedeem(ctx, requestID, func(r *model.RedeemRequest) (*model.CashbackEntry, error) {
		if r.Status == status {
			return nil, nil // resolusi ulang yang sama: tidak ada yang berubah
		}
		if r.Status != model.RedeemPending {
			return nil, apierr.Conflict("redeem_already_resolved", "Request sudah dituntaskan dengan status berbeda")
		}

		r.Status = status
		r.ResolvedAt = &now
		if status != model.RedeemRejected {
			return nil, nil
		}
		return &model.CashbackEntry{
			EntryID:   s.newID("cbe"),
			UserID:    r.UserID,
			RequestID: r.RequestID,
			Kind:      model.CashbackRedeemReturn,
			Amount:    r.Amount,
			CreatedAt: now,
		}, nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return model.RedeemRequest{}, apierr.NotFound("redeem_not_found", "Request redeem tidak ditemukan")
	}
	return resolved, err
}

// ListRedeems mengembalikan request redeem, terbaru dulu. userID 0 berarti
// semua pemain — dipakai tim internal untuk mengambil antrean pending.
func (s *Cashback) ListRedeems(ctx context.Context, userID int64, status, rawCursor string, limit int) (RedeemPage, error) {
	switch status {
	case "", model.RedeemPending, model.RedeemCompleted, model.RedeemRejected:
	default:
		return RedeemPage{}, apierr.BadRequest("invalid_status", "status harus pending, completed, atau rejected")
	}

	after, err := decodeCursor(rawCursor)
	if err != nil {
		return RedeemPage{}, err
	}

	limit = paging.ClampLimit(limit, DefaultPageSize, MaxPageSize)
	rows, hasMore, err := s.cashback.ListRedeems(ctx, store.RedeemFilter{UserID: userID, Status: status}, after, limit)
	if err != nil {
		return RedeemPage{}, err
	}

	page := RedeemPage{Redeems: rows, HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = paging.Encode(paging.KeysetCursor{At: last.RequestedAt, ID: last.RequestID})
		if err != nil {
			return RedeemPage{}, err
		}
	}
	return page, nil
}

// Reverse menarik kembali cashback sebuah transaksi yang di-refund atau kena
// chargeback. Saldo pemain boleh menjadi minus — itu disengaja, sesuai spec.
//
// Idempoten: reversal kedua untuk transaksi yang sama mengembalikan baris
// lama dengan replay bernilai true.
func (s *Cashback) Reverse(ctx context.Context, txID string) (model.CashbackEntry, bool, error) {
	if txID == "" {
		return model.CashbackEntry{}, false, apierr.Unprocessable("missing_tx_id", "Field txId wajib diisi")
	}

	accrual, ok, err := s.cashback.EntryByTxKind(ctx, txID, model.CashbackAccrual)
	if err != nil {
		return model.CashbackEntry{}, false, err
	}
	if !ok {
		return model.CashbackEntry{}, false, apierr.NotFound("cashback_not_found", "Transaksi ini tidak punya cashback yang bisa ditarik")
	}

	entry := model.CashbackEntry{
		EntryID:   s.newID("cbe"),
		UserID:    accrual.UserID,
		TxID:      txID,
		Kind:      model.CashbackReversal,
		Amount:    -accrual.Amount,
		CreatedAt: s.now(),
	}
	if err := s.cashback.CreateEntry(ctx, entry); err != nil {
		if errors.Is(err, store.ErrDuplicateEntry) {
			existing, ok, err := s.cashback.EntryByTxKind(ctx, txID, model.CashbackReversal)
			if err != nil || !ok {
				return model.CashbackEntry{}, false, err
			}
			return existing, true, nil
		}
		return model.CashbackEntry{}, false, err
	}
	return entry, false, nil
}

// CreateEventInput adalah muatan POST /v1/cashback/events.
type CreateEventInput struct {
	Name     string
	StartsAt time.Time
	EndsAt   time.Time
}

// CreateEvent menjadwalkan satu jendela bonus event.
func (s *Cashback) CreateEvent(ctx context.Context, in CreateEventInput) (model.CashbackEvent, error) {
	if in.StartsAt.IsZero() || in.EndsAt.IsZero() {
		return model.CashbackEvent{}, apierr.Unprocessable("missing_window", "Field startsAt dan endsAt wajib diisi")
	}
	if !in.EndsAt.After(in.StartsAt) {
		return model.CashbackEvent{}, apierr.Unprocessable("invalid_window", "endsAt harus setelah startsAt")
	}

	ev := model.CashbackEvent{
		EventID:  s.newID("evt"),
		Name:     in.Name,
		StartsAt: in.StartsAt.UTC(),
		EndsAt:   in.EndsAt.UTC(),
	}
	if err := s.cashback.CreateEvent(ctx, ev); err != nil {
		return model.CashbackEvent{}, err
	}
	return ev, nil
}

// ListEvents mengembalikan event yang masih berjalan atau akan datang.
func (s *Cashback) ListEvents(ctx context.Context) ([]model.CashbackEvent, error) {
	return s.cashback.ListEvents(ctx, s.now())
}

// Reconcile mengagregat ledger pada rentang [from, to). Rentang kosong berarti
// bulan kalender berjalan (UTC) — pas untuk rekonsiliasi bulanan.
func (s *Cashback) Reconcile(ctx context.Context, from, to time.Time) (ReconciliationReport, error) {
	now := s.now()
	if from.IsZero() {
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	if to.IsZero() {
		to = now
	}
	if !to.After(from) {
		return ReconciliationReport{}, apierr.BadRequest("invalid_range", "Parameter to harus setelah from")
	}

	totals, err := s.cashback.Totals(ctx, from, to)
	if err != nil {
		return ReconciliationReport{}, err
	}
	return ReconciliationReport{From: from, To: to, Totals: totals}, nil
}

// decodeCursor membaca cursor keyset dari string buram; kosong berarti halaman
// pertama (nil).
func decodeCursor(raw string) (*paging.KeysetCursor, error) {
	var cursor paging.KeysetCursor
	present, err := paging.Decode(raw, &cursor)
	if err != nil {
		return nil, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
	}
	if !present {
		return nil, nil
	}
	return &cursor, nil
}
