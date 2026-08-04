package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
)

// --- OUTFIT ---------------------------------------------------------------

// MemoryOutfits adalah implementasi in-memory dari Outfits.
type MemoryOutfits struct {
	mu   sync.RWMutex
	rows map[string]model.Outfit
}

// NewMemoryOutfits membuat penyimpanan outfit kosong.
func NewMemoryOutfits() *MemoryOutfits {
	return &MemoryOutfits{rows: make(map[string]model.Outfit)}
}

var _ Outfits = (*MemoryOutfits)(nil)

// Create menyimpan outfit baru.
func (s *MemoryOutfits) Create(_ context.Context, o model.Outfit) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rows[o.OutfitID] = cloneOutfit(o)
	return nil
}

// Get mengembalikan outfit apa adanya, termasuk yang sudah di-soft-delete.
func (s *MemoryOutfits) Get(_ context.Context, outfitID string) (model.Outfit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	o, ok := s.rows[outfitID]
	if !ok {
		return model.Outfit{}, ErrNotFound
	}
	return cloneOutfit(o), nil
}

// List mengembalikan satu halaman outfit hidup yang cocok dengan filter.
func (s *MemoryOutfits) List(_ context.Context, f OutfitFilter, after *paging.KeysetCursor, limit int) ([]model.Outfit, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]model.Outfit, 0, len(s.rows))
	for _, o := range s.rows {
		if o.Deleted() {
			continue
		}
		if f.UserID != 0 && o.UserID != f.UserID {
			continue
		}
		if f.IsPublic != nil && o.IsPublic != *f.IsPublic {
			continue
		}
		if after != nil && !after.After(o.UpdatedAt, o.OutfitID) {
			continue
		}
		matched = append(matched, cloneOutfit(o))
	}
	sortByRecency(matched, func(o model.Outfit) (time.Time, string) { return o.UpdatedAt, o.OutfitID })

	return truncate(matched, limit)
}

// ListByReferenceIDs mengembalikan outfit hidup untuk sekumpulan referenceId.
func (s *MemoryOutfits) ListByReferenceIDs(_ context.Context, referenceIDs []string) ([]model.Outfit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	wanted := make(map[string]struct{}, len(referenceIDs))
	for _, ref := range referenceIDs {
		wanted[ref] = struct{}{}
	}

	found := make([]model.Outfit, 0, len(referenceIDs))
	for _, o := range s.rows {
		if o.Deleted() {
			continue
		}
		if _, ok := wanted[o.ReferenceID]; ok {
			found = append(found, cloneOutfit(o))
		}
	}
	sortByRecency(found, func(o model.Outfit) (time.Time, string) { return o.UpdatedAt, o.OutfitID })
	return found, nil
}

// Update menerapkan fn pada outfit tersimpan. Perubahan hanya disimpan bila fn
// tidak mengembalikan error, sehingga validasi bisa membatalkan penulisan.
func (s *MemoryOutfits) Update(_ context.Context, outfitID string, fn func(*model.Outfit) error) (model.Outfit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.rows[outfitID]
	if !ok {
		return model.Outfit{}, ErrNotFound
	}

	draft := cloneOutfit(o)
	if err := fn(&draft); err != nil {
		return model.Outfit{}, err
	}
	draft.OutfitID = o.OutfitID       // PK tidak boleh berubah
	draft.ReferenceID = o.ReferenceID // referenceId sudah dipegang RecoService
	s.rows[outfitID] = draft
	return cloneOutfit(draft), nil
}

// --- TRANSACTION ----------------------------------------------------------

// MemoryTransactions adalah implementasi in-memory dari Transactions.
type MemoryTransactions struct {
	mu        sync.RWMutex
	rows      map[string]model.Transaction
	byIdemKey map[string]string // idempotencyKey -> txId (kolom UK di ERD)
}

// NewMemoryTransactions membuat penyimpanan transaksi kosong.
func NewMemoryTransactions() *MemoryTransactions {
	return &MemoryTransactions{
		rows:      make(map[string]model.Transaction),
		byIdemKey: make(map[string]string),
	}
}

var _ Transactions = (*MemoryTransactions)(nil)

// Create menyimpan transaksi baru beserta indeks kunci idempotensinya.
func (s *MemoryTransactions) Create(_ context.Context, tx model.Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rows[tx.TxID] = cloneTransaction(tx)
	if tx.IdempotencyKey != "" {
		s.byIdemKey[tx.IdempotencyKey] = tx.TxID
	}
	return nil
}

// ByIdempotencyKey mencari transaksi yang sudah tercatat untuk kunci yang sama.
func (s *MemoryTransactions) ByIdempotencyKey(_ context.Context, key string) (model.Transaction, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	txID, ok := s.byIdemKey[key]
	if !ok {
		return model.Transaction{}, false
	}
	tx, ok := s.rows[txID]
	if !ok {
		return model.Transaction{}, false
	}
	return cloneTransaction(tx), true
}

// ListByUser mengembalikan satu halaman riwayat transaksi pemain.
func (s *MemoryTransactions) ListByUser(_ context.Context, userID int64, after *paging.KeysetCursor, limit int) ([]model.Transaction, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]model.Transaction, 0, len(s.rows))
	for _, tx := range s.rows {
		if tx.UserID != userID {
			continue
		}
		if after != nil && !after.After(tx.OccurredAt, tx.TxID) {
			continue
		}
		matched = append(matched, cloneTransaction(tx))
	}
	sortByRecency(matched, func(tx model.Transaction) (time.Time, string) { return tx.OccurredAt, tx.TxID })

	return truncate(matched, limit)
}

// --- BODY_TEMPLATE --------------------------------------------------------

// MemoryTemplates adalah implementasi in-memory dari Templates.
type MemoryTemplates struct {
	mu   sync.RWMutex
	rows map[string]model.BodyTemplate
}

// NewMemoryTemplates membuat penyimpanan template kosong.
func NewMemoryTemplates() *MemoryTemplates {
	return &MemoryTemplates{rows: make(map[string]model.BodyTemplate)}
}

var _ Templates = (*MemoryTemplates)(nil)

// Get mengembalikan rig dasar berdasarkan templateId.
func (s *MemoryTemplates) Get(_ context.Context, templateID string) (model.BodyTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.rows[templateID]
	if !ok {
		return model.BodyTemplate{}, ErrNotFound
	}
	return t, nil
}

// Ensure mendaftarkan rig bila belum ada, tanpa menimpa yang sudah terdaftar.
func (s *MemoryTemplates) Ensure(_ context.Context, tpl model.BodyTemplate) (model.BodyTemplate, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.rows[tpl.TemplateID]; ok {
		return existing, false, nil
	}
	s.rows[tpl.TemplateID] = tpl
	return tpl, true, nil
}

// List mengembalikan satu halaman rig terdaftar, terbaru dulu.
func (s *MemoryTemplates) List(_ context.Context, after *paging.KeysetCursor, limit int) ([]model.BodyTemplate, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]model.BodyTemplate, 0, len(s.rows))
	for _, t := range s.rows {
		if after != nil && !after.After(t.CreatedAt, t.TemplateID) {
			continue
		}
		matched = append(matched, t)
	}
	sortByRecency(matched, func(t model.BodyTemplate) (time.Time, string) { return t.CreatedAt, t.TemplateID })

	return truncate(matched, limit)
}

// Update menerapkan fn pada rig tersimpan lalu menyimpan hasilnya.
func (s *MemoryTemplates) Update(_ context.Context, templateID string, fn func(*model.BodyTemplate) error) (model.BodyTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.rows[templateID]
	if !ok {
		return model.BodyTemplate{}, ErrNotFound
	}

	draft := current
	if err := fn(&draft); err != nil {
		return model.BodyTemplate{}, err
	}
	draft.TemplateID = current.TemplateID // PK tidak boleh berubah
	s.rows[templateID] = draft
	return draft, nil
}

// --- helper ---------------------------------------------------------------

// sortByRecency mengurutkan menurun berdasarkan waktu, dengan id menaik sebagai
// pemecah seri — urutan yang sama diasumsikan cursor keyset.
func sortByRecency[T any](rows []T, key func(T) (time.Time, string)) {
	sort.Slice(rows, func(i, j int) bool {
		ti, idi := key(rows[i])
		tj, idj := key(rows[j])
		if ti.Equal(tj) {
			return idi < idj
		}
		return ti.After(tj)
	})
}

// truncate memotong rows ke panjang limit dan melaporkan apakah masih ada sisa.
func truncate[T any](rows []T, limit int) ([]T, bool, error) {
	if limit > 0 && len(rows) > limit {
		return rows[:limit], true, nil
	}
	return rows, false, nil
}

func cloneOutfit(o model.Outfit) model.Outfit {
	clone := o
	clone.CustomTags = append([]string(nil), o.CustomTags...)
	clone.Items = append([]model.OutfitItem(nil), o.Items...)
	// Urut slot supaya bentuk respons sama persis dengan penyimpanan Postgres;
	// OUTFIT_ITEM tidak punya kolom urutan, jadi slot yang dipakai.
	sort.Slice(clone.Items, func(i, j int) bool { return clone.Items[i].Slot < clone.Items[j].Slot })
	if o.DeletedAt != nil {
		deletedAt := *o.DeletedAt
		clone.DeletedAt = &deletedAt
	}
	if o.RecoItemID != nil {
		recoItemID := *o.RecoItemID
		clone.RecoItemID = &recoItemID
	}
	return clone
}

func cloneTransaction(tx model.Transaction) model.Transaction {
	clone := tx
	clone.Items = append([]model.TransactionItem(nil), tx.Items...)
	return clone
}
