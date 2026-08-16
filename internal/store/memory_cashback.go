package store

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
)

// MemoryCashback adalah implementasi in-memory dari Cashback.
//
// SpendStats membaca riwayat dari MemoryTransactions yang sama dengan yang
// dipakai service transaksi — sumber datanya memang TRANSACTION +
// TRANSACTION_ITEM, ledger hanya menyimpan hasil hitungnya.
type MemoryCashback struct {
	mu           sync.RWMutex
	entries      map[string]model.CashbackEntry
	redeems      map[string]model.RedeemRequest
	events       map[string]model.CashbackEvent
	transactions *MemoryTransactions
}

// NewMemoryCashback membuat penyimpanan cashback kosong di atas riwayat
// transaksi yang diberikan.
func NewMemoryCashback(transactions *MemoryTransactions) *MemoryCashback {
	return &MemoryCashback{
		entries:      make(map[string]model.CashbackEntry),
		redeems:      make(map[string]model.RedeemRequest),
		events:       make(map[string]model.CashbackEvent),
		transactions: transactions,
	}
}

var _ Cashback = (*MemoryCashback)(nil)

// SpendStats membaca riwayat belanja sukses pemain.
func (s *MemoryCashback) SpendStats(_ context.Context, userID int64, excludeTxID string, daysSince time.Time) (SpendStats, error) {
	s.transactions.mu.RLock()
	defer s.transactions.mu.RUnlock()

	stats := SpendStats{SpendDays: make(map[string]bool)}
	for _, tx := range s.transactions.rows {
		if tx.UserID != userID || tx.TxID == excludeTxID {
			continue
		}
		spend := tx.RobuxTotal()
		if spend == 0 {
			continue
		}
		stats.LifetimeSpend += spend
		stats.PurchaseCount++
		if !tx.OccurredAt.Before(daysSince) {
			stats.SpendDays[tx.OccurredAt.UTC().Format("2006-01-02")] = true
		}
	}
	return stats, nil
}

// CreateEntry menambah satu baris ledger dengan dedup (txId, kind).
func (s *MemoryCashback) CreateEntry(_ context.Context, e model.CashbackEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e.TxID != "" {
		for _, existing := range s.entries {
			if existing.TxID == e.TxID && existing.Kind == e.Kind {
				return ErrDuplicateEntry
			}
		}
	}
	s.entries[e.EntryID] = e
	return nil
}

// EntryByTxKind mencari baris ledger sebuah transaksi untuk kind tertentu.
func (s *MemoryCashback) EntryByTxKind(_ context.Context, txID string, kind model.CashbackKind) (model.CashbackEntry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries {
		if e.TxID == txID && e.Kind == kind {
			return e, true, nil
		}
	}
	return model.CashbackEntry{}, false, nil
}

// Balance menjumlahkan seluruh amount pemain.
func (s *MemoryCashback) Balance(_ context.Context, userID int64) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var total int
	for _, e := range s.entries {
		if e.UserID == userID {
			total += e.Amount
		}
	}
	return total, nil
}

// ListEntries mengembalikan satu halaman ledger pemain, terbaru dulu.
func (s *MemoryCashback) ListEntries(_ context.Context, userID int64, after *paging.KeysetCursor, limit int) ([]model.CashbackEntry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]model.CashbackEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if e.UserID != userID {
			continue
		}
		if after != nil && !after.After(e.CreatedAt, e.EntryID) {
			continue
		}
		matched = append(matched, e)
	}
	sortByRecency(matched, func(e model.CashbackEntry) (time.Time, string) { return e.CreatedAt, e.EntryID })

	return truncate(matched, limit)
}

// ListBalances merangkum ledger per pemain, aktivitas terbaru dulu.
func (s *MemoryCashback) ListBalances(_ context.Context, after *paging.KeysetCursor, limit int) ([]UserBalance, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byUser := make(map[int64]*UserBalance)
	for _, e := range s.entries {
		row, ok := byUser[e.UserID]
		if !ok {
			row = &UserBalance{UserID: e.UserID}
			byUser[e.UserID] = row
		}
		row.Balance += e.Amount
		row.EntryCount++
		switch e.Kind {
		case model.CashbackAccrual:
			row.Accrued += e.Amount
		case model.CashbackRedeem:
			// amount redeem tersimpan negatif; daftar menampilkannya positif.
			row.Redeemed -= e.Amount
		case model.CashbackReversal:
			row.Reversed -= e.Amount
		}
		if e.CreatedAt.After(row.LastEntryAt) {
			row.LastEntryAt = e.CreatedAt
		}
	}

	matched := make([]UserBalance, 0, len(byUser))
	for _, row := range byUser {
		if after != nil && !after.After(row.LastEntryAt, strconv.FormatInt(row.UserID, 10)) {
			continue
		}
		matched = append(matched, *row)
	}
	sortByRecency(matched, func(b UserBalance) (time.Time, string) {
		return b.LastEntryAt, strconv.FormatInt(b.UserID, 10)
	})

	return truncate(matched, limit)
}

// CreateRedeem menyimpan request pending plus baris pemotong saldo sekaligus.
func (s *MemoryCashback) CreateRedeem(_ context.Context, r model.RedeemRequest, deduct model.CashbackEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.redeems {
		if existing.UserID == r.UserID && existing.Status == model.RedeemPending {
			return ErrPendingRedeemExists
		}
	}
	s.redeems[r.RequestID] = cloneRedeem(r)
	s.entries[deduct.EntryID] = deduct
	return nil
}

// GetRedeem mengembalikan satu request redeem.
func (s *MemoryCashback) GetRedeem(_ context.Context, requestID string) (model.RedeemRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	r, ok := s.redeems[requestID]
	if !ok {
		return model.RedeemRequest{}, ErrNotFound
	}
	return cloneRedeem(r), nil
}

// ResolveRedeem menerapkan fn pada request tersimpan lalu menyimpan hasilnya.
func (s *MemoryCashback) ResolveRedeem(_ context.Context, requestID string, fn func(*model.RedeemRequest) (*model.CashbackEntry, error)) (model.RedeemRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.redeems[requestID]
	if !ok {
		return model.RedeemRequest{}, ErrNotFound
	}

	draft := cloneRedeem(r)
	entry, err := fn(&draft)
	if err != nil {
		return model.RedeemRequest{}, err
	}
	draft.RequestID = r.RequestID // PK tidak boleh berubah
	s.redeems[requestID] = cloneRedeem(draft)
	if entry != nil {
		s.entries[entry.EntryID] = *entry
	}
	return draft, nil
}

// ListRedeems mengembalikan satu halaman request yang cocok dengan filter.
func (s *MemoryCashback) ListRedeems(_ context.Context, f RedeemFilter, after *paging.KeysetCursor, limit int) ([]model.RedeemRequest, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]model.RedeemRequest, 0, len(s.redeems))
	for _, r := range s.redeems {
		if f.UserID != 0 && r.UserID != f.UserID {
			continue
		}
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		if after != nil && !after.After(r.RequestedAt, r.RequestID) {
			continue
		}
		matched = append(matched, cloneRedeem(r))
	}
	sortByRecency(matched, func(r model.RedeemRequest) (time.Time, string) { return r.RequestedAt, r.RequestID })

	return truncate(matched, limit)
}

// CreateEvent menjadwalkan satu jendela bonus event.
func (s *MemoryCashback) CreateEvent(_ context.Context, ev model.CashbackEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events[ev.EventID] = ev
	return nil
}

// ActiveEvent mencari event yang jendelanya mencakup at.
func (s *MemoryCashback) ActiveEvent(_ context.Context, at time.Time) (model.CashbackEvent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, ev := range s.events {
		if ev.Active(at) {
			return ev, true, nil
		}
	}
	return model.CashbackEvent{}, false, nil
}

// ListEvents mengembalikan event yang belum berakhir pada from, urut mulai.
func (s *MemoryCashback) ListEvents(_ context.Context, from time.Time) ([]model.CashbackEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]model.CashbackEvent, 0, len(s.events))
	for _, ev := range s.events {
		if ev.EndsAt.After(from) {
			out = append(out, ev)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartsAt.Equal(out[j].StartsAt) {
			return out[i].EventID < out[j].EventID
		}
		return out[i].StartsAt.Before(out[j].StartsAt)
	})
	return out, nil
}

// EventByID mengembalikan satu event.
func (s *MemoryCashback) EventByID(_ context.Context, eventID string) (model.CashbackEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ev, ok := s.events[eventID]
	if !ok {
		return model.CashbackEvent{}, ErrNotFound
	}
	return ev, nil
}

// UpdateEvent menimpa nama dan jendela sebuah event.
func (s *MemoryCashback) UpdateEvent(_ context.Context, ev model.CashbackEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.events[ev.EventID]; !ok {
		return ErrNotFound
	}
	s.events[ev.EventID] = ev
	return nil
}

// DeleteEvent menghapus event.
func (s *MemoryCashback) DeleteEvent(_ context.Context, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.events[eventID]; !ok {
		return ErrNotFound
	}
	delete(s.events, eventID)
	return nil
}

// Totals mengagregat ledger pada rentang [from, to).
func (s *MemoryCashback) Totals(_ context.Context, from, to time.Time) (model.CashbackTotals, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var totals model.CashbackTotals
	for _, e := range s.entries {
		totals.Outstanding += e.Amount
		if e.CreatedAt.Before(from) || !e.CreatedAt.Before(to) {
			continue
		}
		switch e.Kind {
		case model.CashbackAccrual:
			totals.Accrued += e.Amount
		case model.CashbackReversal:
			totals.Reversed -= e.Amount
		case model.CashbackRedeem:
			totals.Redeemed -= e.Amount
		case model.CashbackRedeemReturn:
			totals.Returned += e.Amount
		}
	}
	return totals, nil
}

func cloneRedeem(r model.RedeemRequest) model.RedeemRequest {
	clone := r
	if r.ResolvedAt != nil {
		resolvedAt := *r.ResolvedAt
		clone.ResolvedAt = &resolvedAt
	}
	return clone
}
