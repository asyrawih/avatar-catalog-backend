package store

import (
	"cmp"
	"context"
	"slices"
	"sort"
	"strings"
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
	// embeddings hidup terpisah dari rows: embedding bukan bagian bentuk API
	// outfit, cuma bahan peringkat pencarian.
	embeddings map[string][]float32
	// likes meniru tabel OUTFIT_LIKE: satu pemain satu like per outfit.
	likes map[string]map[int64]time.Time
	// views meniru tabel OUTFIT_VIEW yang append-only. Disimpan penuh, bukan
	// cuma dihitung, supaya perilakunya sama dengan Postgres — termasuk saat
	// dipakai menyiapkan data latih dari mode in-memory.
	views map[string][]outfitView
}

// outfitView adalah satu kejadian lihat.
type outfitView struct {
	UserID   int64 // 0 = anonim
	ViewedAt time.Time
}

// NewMemoryOutfits membuat penyimpanan outfit kosong.
func NewMemoryOutfits() *MemoryOutfits {
	return &MemoryOutfits{
		rows:       make(map[string]model.Outfit),
		embeddings: make(map[string][]float32),
		likes:      make(map[string]map[int64]time.Time),
		views:      make(map[string][]outfitView),
	}
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
func (s *MemoryOutfits) List(_ context.Context, f OutfitFilter, after *OutfitCursor, limit int) ([]model.Outfit, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	wantedIDs := make(map[string]struct{}, len(f.OutfitIDs))
	for _, id := range f.OutfitIDs {
		wantedIDs[id] = struct{}{}
	}
	keyword := strings.ToLower(f.Keyword)

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
		if len(wantedIDs) > 0 {
			if _, ok := wantedIDs[o.OutfitID]; !ok {
				continue
			}
		}
		if keyword != "" && !strings.Contains(strings.ToLower(o.Name), keyword) {
			continue
		}
		if !afterCursor(after, f.Sort, o) {
			continue
		}
		matched = append(matched, cloneOutfit(o))
	}
	sortOutfits(matched, f.Sort)

	return truncate(matched, limit)
}

// afterCursor melaporkan apakah outfit berada setelah posisi cursor pada
// urutan sort. Cursor yang jenisnya tidak cocok dengan sort diabaikan, bukan
// dipaksakan: memakai kunci yang salah akan membuang baris yang benar.
func afterCursor(after *OutfitCursor, sort OutfitSort, o model.Outfit) bool {
	if after == nil {
		return true
	}
	if sort.ByCount() {
		if after.Count == nil {
			return true
		}
		return after.Count.After(sortCount(o, sort), o.OutfitID)
	}
	if after.Recency == nil {
		return true
	}
	return after.Recency.After(o.UpdatedAt, o.OutfitID)
}

// sortCount mengambil pencacah yang dipakai sort.
func sortCount(o model.Outfit, sort OutfitSort) int {
	if sort == SortMostViewed {
		return o.ViewCount
	}
	return o.LikeCount
}

// sortOutfits mengurutkan menurun sesuai sort, dengan outfitId menaik sebagai
// pemecah seri — sama persis dengan ORDER BY di Postgres, supaya cursor yang
// diterbitkan satu implementasi tetap sahih di implementasi lain.
func sortOutfits(rows []model.Outfit, sort OutfitSort) {
	if !sort.ByCount() {
		sortByRecency(rows, func(o model.Outfit) (time.Time, string) { return o.UpdatedAt, o.OutfitID })
		return
	}
	slices.SortFunc(rows, func(a, b model.Outfit) int {
		if n := cmp.Compare(sortCount(b, sort), sortCount(a, sort)); n != 0 {
			return n
		}
		return cmp.Compare(a.OutfitID, b.OutfitID)
	})
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

// Search mengembalikan outfit hidup terurut dari yang paling mirip dengan
// f.Keyword, memakai kemiripan trigram yang meniru word_similarity pg_trgm
// dengan ambang 0.3 yang sama. qEmbedding diabaikan: penyimpanan in-memory
// tidak menyimpan embedding sebagai bahan peringkat, dan kontraknya memang
// membolehkan pencarian leksikal saja.
func (s *MemoryOutfits) Search(_ context.Context, f OutfitFilter, _ []float32, limit int) ([]model.Outfit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type scored struct {
		outfit model.Outfit
		sim    float64
	}

	var matched []scored
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
		sim := wordSimilarity(f.Keyword, o.Name)
		if sim < 0.3 {
			continue
		}
		matched = append(matched, scored{outfit: cloneOutfit(o), sim: sim})
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].sim != matched[j].sim {
			return matched[i].sim > matched[j].sim
		}
		if !matched[i].outfit.UpdatedAt.Equal(matched[j].outfit.UpdatedAt) {
			return matched[i].outfit.UpdatedAt.After(matched[j].outfit.UpdatedAt)
		}
		return matched[i].outfit.OutfitID < matched[j].outfit.OutfitID
	})

	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	out := make([]model.Outfit, 0, len(matched))
	for _, m := range matched {
		out = append(out, m.outfit)
	}
	return out, nil
}

// SetNameEmbedding menyimpan embedding nama sebuah outfit.
func (s *MemoryOutfits) SetNameEmbedding(_ context.Context, outfitID string, embedding []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.rows[outfitID]; !ok {
		return ErrNotFound
	}
	s.embeddings[outfitID] = append([]float32(nil), embedding...)
	return nil
}

// Like mencatat like dan menaikkan like_count. Idempoten.
func (s *MemoryOutfits) Like(_ context.Context, outfitID string, userID int64, at time.Time) (EngagementCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.rows[outfitID]
	if !ok || o.Deleted() {
		return EngagementCounts{}, ErrNotFound
	}
	if _, already := s.likes[outfitID][userID]; already {
		return s.countsLocked(o), nil
	}

	if s.likes[outfitID] == nil {
		s.likes[outfitID] = make(map[int64]time.Time)
	}
	s.likes[outfitID][userID] = at
	o.LikeCount = len(s.likes[outfitID])
	s.rows[outfitID] = o

	counts := s.countsLocked(o)
	counts.Changed = true
	return counts, nil
}

// Unlike menghapus like dan menurunkan like_count.
func (s *MemoryOutfits) Unlike(_ context.Context, outfitID string, userID int64) (EngagementCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.rows[outfitID]
	if !ok || o.Deleted() {
		return EngagementCounts{}, ErrNotFound
	}
	if _, liked := s.likes[outfitID][userID]; !liked {
		return s.countsLocked(o), nil
	}

	delete(s.likes[outfitID], userID)
	o.LikeCount = len(s.likes[outfitID])
	s.rows[outfitID] = o

	counts := s.countsLocked(o)
	counts.Changed = true
	return counts, nil
}

// RecordView menambahkan satu kejadian lihat dan menaikkan view_count.
func (s *MemoryOutfits) RecordView(_ context.Context, outfitID string, userID int64, at time.Time) (EngagementCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.rows[outfitID]
	if !ok || o.Deleted() {
		return EngagementCounts{}, ErrNotFound
	}

	s.views[outfitID] = append(s.views[outfitID], outfitView{UserID: userID, ViewedAt: at})
	o.ViewCount = len(s.views[outfitID])
	s.rows[outfitID] = o

	counts := s.countsLocked(o)
	counts.Changed = true
	return counts, nil
}

// countsLocked membaca popularitas dari baris yang sudah diperbarui. Pemanggil
// wajib memegang s.mu.
func (s *MemoryOutfits) countsLocked(o model.Outfit) EngagementCounts {
	return EngagementCounts{LikeCount: o.LikeCount, ViewCount: o.ViewCount}
}

// Liked melaporkan outfit mana saja dari outfitIDs yang sudah disukai userID.
func (s *MemoryOutfits) Liked(_ context.Context, userID int64, outfitIDs []string) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]bool, len(outfitIDs))
	for _, id := range outfitIDs {
		if _, ok := s.likes[id][userID]; ok {
			out[id] = true
		}
	}
	return out, nil
}

// wordSimilarity mendekati word_similarity pg_trgm: kemiripan terbaik antara
// query dengan nama utuh maupun tiap katanya, dihitung sebagai Jaccard atas
// himpunan trigram. Cukup dekat untuk test dan mode tanpa Postgres; peringkat
// persisnya tetap milik implementasi Postgres.
func wordSimilarity(query, name string) float64 {
	q := trigrams(query)
	if len(q) == 0 {
		return 0
	}

	best := trigramJaccard(q, trigrams(name))
	for word := range strings.FieldsSeq(name) {
		if sim := trigramJaccard(q, trigrams(word)); sim > best {
			best = sim
		}
	}
	return best
}

// trigrams membentuk himpunan trigram gaya pg_trgm: huruf kecil, tiap kata
// diberi dua spasi pembuka dan satu penutup.
func trigrams(text string) map[string]struct{} {
	out := make(map[string]struct{})
	for word := range strings.FieldsSeq(strings.ToLower(text)) {
		padded := []rune("  " + word + " ")
		for i := 0; i+3 <= len(padded); i++ {
			out[string(padded[i:i+3])] = struct{}{}
		}
	}
	return out
}

func trigramJaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	shared := 0
	for t := range a {
		if _, ok := b[t]; ok {
			shared++
		}
	}
	if shared == 0 {
		return 0
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
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
	if o.Body != nil {
		body := *o.Body
		if o.Body.Colors != nil {
			colors := *o.Body.Colors
			body.Colors = &colors
		}
		if o.Body.Scales != nil {
			scales := *o.Body.Scales
			body.Scales = &scales
		}
		clone.Body = &body
	}
	return clone
}

func cloneTransaction(tx model.Transaction) model.Transaction {
	clone := tx
	clone.Items = append([]model.TransactionItem(nil), tx.Items...)
	return clone
}
