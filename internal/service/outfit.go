package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Batas yang dipakai endpoint outfit.
const (
	// MaxResolveIDs membatasi panjang daftar referenceId yang boleh dikirim ke
	// resolve. Batasnya jauh di atas satu halaman karena hasilnya berhalaman:
	// yang dikirim adalah seluruh feed, yang diambil hanya sepotong.
	MaxResolveIDs   = 500
	MaxOutfitItems  = 30
	MaxCustomTags   = 20
	DefaultPageSize = 20
	MaxPageSize     = 100

	// MaxKeywordLen membatasi kata kunci pencarian nama outfit. Nama outfit
	// sendiri dibatasi lebih pendek, jadi kata kunci yang lebih panjang dari
	// ini pasti tidak cocok dengan apa pun.
	MaxKeywordLen = 120

	// MaxOutfitIDs membatasi jumlah outfitId yang boleh diminta sekaligus,
	// sejalan dengan batas resolve.
	MaxOutfitIDs = 100

	// MaxItemNameLen membatasi nama item yang dilaporkan klien.
	MaxItemNameLen = 120

	// MaxAssetTypeLen membatasi assetType. Nilainya berasal dari enum AssetType
	// Roblox ("Accessory", "Shirt", ...), jadi batas ini cuma pagar terhadap
	// muatan ngawur, bukan daftar putih — enum itu bertambah tanpa kabar.
	MaxAssetTypeLen = 40
)

// Actor adalah pemanggil di balik sebuah request.
//
// Untuk sekarang isinya berasal dari header dan belum diverifikasi — lihat
// httpapi.Authenticator. Begitu autentikasi asli dipasang, UserID datang dari
// token dan pemeriksaan kepemilikan di bawah langsung berlaku tanpa diubah.
type Actor struct {
	UserID  int64
	Present bool
}

// OutfitPage adalah satu halaman daftar outfit.
type OutfitPage struct {
	Outfits    []model.Outfit
	NextCursor string
	HasMore    bool
}

// CreateOutfitInput adalah muatan POST /v1/outfits.
type CreateOutfitInput struct {
	UserID     int64
	TemplateID string
	Name       string
	IsPublic   bool
	CustomTags []string
	Items      []model.OutfitItem
	Body       *model.AvatarBody // opsional
	// ThumbnailAssetID opsional: asset id thumbnail hasil upload game server.
	ThumbnailAssetID int64
}

// UpdateOutfitInput adalah muatan PATCH /v1/outfits/{outfitId}; field nil
// dibiarkan apa adanya.
type UpdateOutfitInput struct {
	Name       *string
	IsPublic   *bool
	CustomTags *[]string
	RecoItemID *string
	TemplateID *string
}

// Outfits menyediakan operasi outfit pemain.
type Outfits struct {
	outfits   store.Outfits
	templates store.Templates
	// embedder opsional; nil berarti pencarian leksikal saja. Lihat embed.go.
	embedder Embedder
	// embedSlots memplafon jumlah embedding yang berjalan bersamaan.
	embedSlots chan struct{}
	now        func() time.Time
	newID      func() string
	newRefID   func() string
}

// NewOutfits merangkai service outfit.
func NewOutfits(outfits store.Outfits, templates store.Templates) *Outfits {
	return &Outfits{
		outfits:    outfits,
		templates:  templates,
		embedSlots: make(chan struct{}, maxConcurrentEmbeds),
		now:        func() time.Time { return time.Now().UTC() },
		newID:      newOutfitID,
		newRefID:   newUUID,
	}
}

// WithEmbedder memasang penyedia embedding untuk pencarian makna lintas bahasa
// dan mengembalikan service yang sama agar bisa dirangkai saat perakitan.
func (s *Outfits) WithEmbedder(e Embedder) *Outfits {
	s.embedder = e
	return s
}

// ListOutfitFilter menyaring daftar outfit dari sisi API.
type ListOutfitFilter struct {
	UserID    int64    // 0 = semua pemain
	IsPublic  *bool    // nil = publik dan privat
	OutfitIDs []string // kosong = semua outfit
	Keyword   string   // kosong = tanpa pencarian nama
	// Sort apa adanya dari query string; divalidasi di List.
	Sort string
}

// parseOutfitSort menerjemahkan nilai query string menjadi urutan yang
// dikenali penyimpanan. Kosong berarti urutan bawaan, terbaru dulu.
func parseOutfitSort(raw string) (store.OutfitSort, error) {
	switch raw {
	case "", "recent":
		return store.SortRecent, nil
	case string(store.SortMostLiked):
		return store.SortMostLiked, nil
	case string(store.SortMostViewed):
		return store.SortMostViewed, nil
	default:
		return "", apierr.BadRequest("invalid_sort",
			"Parameter sort harus salah satu dari: recent, mostLiked, mostViewed")
	}
}

// List mengembalikan outfit, terbaru dulu.
//
// userId opsional: tanpa userId daftar mencakup semua pemain, yang berguna
// untuk feed dan untuk memeriksa isi katalog saat pengembangan.
func (s *Outfits) List(ctx context.Context, f ListOutfitFilter, rawCursor string, limit int) (OutfitPage, error) {
	if f.UserID < 0 {
		return OutfitPage{}, apierr.BadRequest("invalid_user_id", "Parameter userId tidak valid")
	}

	keyword := strings.TrimSpace(f.Keyword)
	if len([]rune(keyword)) > MaxKeywordLen {
		return OutfitPage{}, apierr.BadRequest("invalid_keyword", fmt.Sprintf("Kata kunci maksimal %d karakter", MaxKeywordLen))
	}
	if len(f.OutfitIDs) > MaxOutfitIDs {
		return OutfitPage{}, apierr.BadRequest("too_many_outfit_ids", fmt.Sprintf("Maksimal %d outfitId per permintaan", MaxOutfitIDs))
	}

	sort, err := parseOutfitSort(f.Sort)
	if err != nil {
		return OutfitPage{}, err
	}

	after, err := decodeListCursor(rawCursor, sort)
	if err != nil {
		return OutfitPage{}, err
	}

	limit = paging.ClampLimit(limit, DefaultPageSize, MaxPageSize)
	rows, hasMore, err := s.outfits.List(ctx, store.OutfitFilter{
		UserID:    f.UserID,
		IsPublic:  f.IsPublic,
		OutfitIDs: f.OutfitIDs,
		Keyword:   keyword,
		Sort:      sort,
	}, after, limit)
	if err != nil {
		return OutfitPage{}, err
	}

	page := OutfitPage{Outfits: rows, HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		page.NextCursor, err = encodeListCursor(rows[len(rows)-1], sort)
		if err != nil {
			return OutfitPage{}, err
		}
	}
	return page, nil
}

// listCursor adalah bentuk cursor daftar outfit yang dikirim ke klien.
//
// Sort ikut dibawa karena kunci paginasi berbeda per urutan: cursor "terbaru"
// yang dipakai pada urutan "terpopuler" akan menyaring baris dengan kunci yang
// salah dan diam-diam mengembalikan halaman ngawur. Dengan sort tersimpan di
// dalamnya, ketidakcocokan itu jadi error yang kelihatan.
//
// Cursor lama dari sebelum ada pengurutan tidak punya field sort; nilainya
// kosong dan itu berarti urutan bawaan, jadi cursor yang sudah beredar tetap
// sah.
type listCursor struct {
	Sort  string     `json:"sort,omitempty"`
	At    *time.Time `json:"at,omitempty"`
	Count *int       `json:"count,omitempty"`
	ID    string     `json:"id"`
}

func decodeListCursor(raw string, sort store.OutfitSort) (*store.OutfitCursor, error) {
	var c listCursor
	present, err := paging.Decode(raw, &c)
	if err != nil {
		return nil, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
	}
	if !present {
		return nil, nil
	}
	if c.Sort != string(sort) {
		return nil, apierr.BadRequest("cursor_sort_mismatch",
			"Cursor ini milik urutan lain; ulangi dari halaman pertama setelah mengubah sort")
	}

	if sort.ByCount() {
		if c.Count == nil {
			return nil, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
		}
		return &store.OutfitCursor{Count: &paging.CountCursor{Count: *c.Count, ID: c.ID}}, nil
	}
	if c.At == nil {
		return nil, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
	}
	return &store.OutfitCursor{Recency: &paging.KeysetCursor{At: *c.At, ID: c.ID}}, nil
}

func encodeListCursor(last model.Outfit, sort store.OutfitSort) (string, error) {
	c := listCursor{Sort: string(sort), ID: last.OutfitID}
	switch sort {
	case store.SortMostLiked:
		count := last.LikeCount
		c.Count = &count
	case store.SortMostViewed:
		count := last.ViewCount
		c.Count = &count
	default:
		at := last.UpdatedAt
		c.At = &at
	}
	return paging.Encode(c)
}

// SearchOutfitFilter menyaring pencarian outfit dari sisi API.
type SearchOutfitFilter struct {
	Query    string
	UserID   int64 // 0 = semua pemain
	IsPublic *bool // nil = publik dan privat
}

// Search mengembalikan outfit terurut dari yang paling mirip dengan Query —
// toleran salah ketik lewat trigram, dan bila embedder terpasang juga mirip
// secara makna lintas bahasa. Hasilnya peringkat, bukan halaman: pencarian
// yang relevan habis di urutan awal, jadi tidak ada cursor.
func (s *Outfits) Search(ctx context.Context, f SearchOutfitFilter, limit int) ([]model.Outfit, error) {
	if f.UserID < 0 {
		return nil, apierr.BadRequest("invalid_user_id", "Parameter userId tidak valid")
	}

	query := strings.TrimSpace(f.Query)
	if len([]rune(query)) < 2 {
		return nil, apierr.BadRequest("invalid_query", "Parameter q minimal 2 karakter")
	}
	if len([]rune(query)) > MaxKeywordLen {
		return nil, apierr.BadRequest("invalid_query", fmt.Sprintf("Parameter q maksimal %d karakter", MaxKeywordLen))
	}

	limit = paging.ClampLimit(limit, DefaultPageSize, MaxPageSize)
	return s.outfits.Search(ctx, store.OutfitFilter{
		UserID:   f.UserID,
		IsPublic: f.IsPublic,
		Keyword:  query,
	}, s.queryEmbedding(ctx, query), limit)
}

// Get mengembalikan satu outfit lengkap dengan itemnya.
func (s *Outfits) Get(ctx context.Context, outfitID string) (model.Outfit, error) {
	return s.load(ctx, outfitID)
}

// Create membuat outfit baru. Backend yang membangkitkan referenceId, karena
// nilai itulah yang dikirim ke RegisterItemAsync — bukan outfitId internal.
func (s *Outfits) Create(ctx context.Context, in CreateOutfitInput) (model.Outfit, error) {
	outfit, err := s.buildNewOutfit(ctx, in)
	if err != nil {
		return model.Outfit{}, err
	}

	if err := s.outfits.Create(ctx, outfit); err != nil {
		return model.Outfit{}, err
	}
	s.embedNameAsync(outfit.OutfitID, outfit.Name)
	return outfit, nil
}

// buildNewOutfit memeriksa muatan dan menyusun baris outfit yang siap ditulis,
// tanpa menulisnya. Dipakai bersama oleh Create dan CreateBatch supaya satu
// outfit divalidasi dengan aturan yang persis sama, mau dikirim sendirian
// maupun dalam batch.
//
// Satu-satunya efek sampingnya adalah pendaftaran rig lewat ensureTemplate —
// lihat komentar di sana.
func (s *Outfits) buildNewOutfit(ctx context.Context, in CreateOutfitInput) (model.Outfit, error) {
	if in.UserID <= 0 {
		return model.Outfit{}, apierr.Unprocessable("missing_user_id", "Field userId wajib diisi")
	}
	if strings.TrimSpace(in.Name) == "" {
		return model.Outfit{}, apierr.Unprocessable("missing_name", "Field name wajib diisi")
	}
	if err := s.ensureTemplate(ctx, in.TemplateID); err != nil {
		return model.Outfit{}, err
	}
	if err := validateCustomTags(in.CustomTags); err != nil {
		return model.Outfit{}, err
	}
	if err := s.validateItems(in.Items); err != nil {
		return model.Outfit{}, err
	}
	body, err := normalizeBody(in.Body)
	if err != nil {
		return model.Outfit{}, err
	}

	if in.ThumbnailAssetID < 0 {
		return model.Outfit{}, apierr.Unprocessable("invalid_thumbnail_asset_id", "thumbnailAssetId tidak boleh negatif")
	}

	now := s.now()
	outfit := model.Outfit{
		OutfitID:         s.newID(),
		ReferenceID:      s.newRefID(),
		UserID:           in.UserID,
		TemplateID:       strings.TrimSpace(in.TemplateID),
		Name:             strings.TrimSpace(in.Name),
		IsPublic:         in.IsPublic,
		CustomTags:       in.CustomTags,
		Items:            in.Items,
		Body:             body,
		ThumbnailAssetID: in.ThumbnailAssetID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if outfit.CustomTags == nil {
		outfit.CustomTags = []string{}
	}
	if outfit.Items == nil {
		outfit.Items = []model.OutfitItem{}
	}
	return outfit, nil
}

// Update mengubah sebagian metadata outfit, termasuk menyimpan recoItemId
// hasil RegisterItemAsync.
func (s *Outfits) Update(ctx context.Context, actor Actor, outfitID string, in UpdateOutfitInput) (model.Outfit, error) {
	current, err := s.load(ctx, outfitID)
	if err != nil {
		return model.Outfit{}, err
	}
	if err := ensureOwner(actor, current); err != nil {
		return model.Outfit{}, err
	}
	if in.CustomTags != nil {
		if err := validateCustomTags(*in.CustomTags); err != nil {
			return model.Outfit{}, err
		}
	}
	if in.TemplateID != nil {
		if err := s.ensureTemplate(ctx, *in.TemplateID); err != nil {
			return model.Outfit{}, err
		}
		trimmed := strings.TrimSpace(*in.TemplateID)
		in.TemplateID = &trimmed
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return model.Outfit{}, apierr.Unprocessable("missing_name", "Field name tidak boleh kosong")
	}

	updated, err := s.outfits.Update(ctx, outfitID, func(o *model.Outfit) error {
		if in.Name != nil {
			o.Name = strings.TrimSpace(*in.Name)
		}
		if in.IsPublic != nil {
			o.IsPublic = *in.IsPublic
		}
		if in.CustomTags != nil {
			o.CustomTags = *in.CustomTags
		}
		if in.RecoItemID != nil {
			recoItemID := *in.RecoItemID
			o.RecoItemID = &recoItemID
		}
		if in.TemplateID != nil {
			o.TemplateID = *in.TemplateID
		}
		o.UpdatedAt = s.now()
		return nil
	})
	if err != nil {
		return model.Outfit{}, err
	}
	if in.Name != nil && updated.Name != current.Name {
		s.embedNameAsync(updated.OutfitID, updated.Name)
	}
	return updated, nil
}

// ReplaceItems mengganti seluruh isi item outfit.
//
// Sengaja mengganti seluruh koleksi, bukan menambal sebagian: item outfit
// adalah himpunan, dan mengganti seluruhnya membuat operasi ini idempoten
// sehingga aman diulang saat retry.
func (s *Outfits) ReplaceItems(ctx context.Context, actor Actor, outfitID string, items []model.OutfitItem) (model.Outfit, error) {
	current, err := s.load(ctx, outfitID)
	if err != nil {
		return model.Outfit{}, err
	}
	if err := ensureOwner(actor, current); err != nil {
		return model.Outfit{}, err
	}
	if err := s.validateItems(items); err != nil {
		return model.Outfit{}, err
	}

	return s.outfits.Update(ctx, outfitID, func(o *model.Outfit) error {
		o.Items = items
		if o.Items == nil {
			o.Items = []model.OutfitItem{}
		}
		o.UpdatedAt = s.now()
		return nil
	})
}

// SoftDelete mengisi deletedAt tanpa menghapus baris.
//
// Setelah ini pemanggil wajib memanggil RecommendationService:RemoveItemAsync
// dengan recoItemId yang dikembalikan, kalau tidak outfit yang sudah dihapus
// tetap muncul di feed dan referenceId-nya menggantung.
func (s *Outfits) SoftDelete(ctx context.Context, actor Actor, outfitID string) (model.Outfit, error) {
	current, err := s.load(ctx, outfitID)
	if err != nil {
		return model.Outfit{}, err
	}
	if err := ensureOwner(actor, current); err != nil {
		return model.Outfit{}, err
	}

	now := s.now()
	return s.outfits.Update(ctx, outfitID, func(o *model.Outfit) error {
		o.DeletedAt = &now
		o.UpdatedAt = now
		return nil
	})
}

// ResolvePage adalah satu halaman hasil resolve.
//
// NotFound hanya memuat referenceId dari halaman ini, bukan dari seluruh
// daftar — id di halaman yang belum diminta belum pernah dicari, jadi
// melaporkannya sebagai "tidak ditemukan" akan menyesatkan.
// Total dan TotalPages bisa dilaporkan pasti karena yang dinomori adalah
// daftar referenceId yang klien kirim sendiri — tidak perlu menghitung isi
// database.
type ResolvePage struct {
	Found      []model.Outfit
	NotFound   []string
	NextCursor string
	HasMore    bool
	Total      int
	TotalPages int
}

// Resolve menukar sekumpulan referenceId dari feed rekomendasi menjadi
// metadata render.
//
// Feed bisa membawa ratusan referenceId sekaligus, sementara satu outfit
// membawa seluruh item dan body-nya. Karena itu hasilnya berhalaman: cursor
// menandai posisi di dalam referenceIds yang dikirim klien, dan tiap
// permintaan hanya mengambil sepotong itu dari penyimpanan. Klien mengirim
// daftar id yang sama persis di tiap halaman.
func (s *Outfits) Resolve(ctx context.Context, referenceIDs []string, rawCursor string, limit int) (ResolvePage, error) {
	if len(referenceIDs) > MaxResolveIDs {
		return ResolvePage{}, apierr.TooLarge("too_many_ids", fmt.Sprintf("Maksimum %d referenceId per permintaan", MaxResolveIDs))
	}

	var cursor paging.PositionCursor
	if _, err := paging.Decode(rawCursor, &cursor); err != nil {
		return ResolvePage{}, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
	}
	if cursor.Pos < 0 {
		return ResolvePage{}, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
	}

	limit = paging.ClampLimit(limit, DefaultPageSize, MaxPageSize)

	total := len(referenceIDs)
	// Pembagian dibulatkan ke atas: sisa berapa pun tetap satu halaman.
	totalPages := (total + limit - 1) / limit

	// Cursor yang sudah melewati ujung daftar berarti halaman habis. Ini juga
	// menjaga daftar yang menyusut di antara dua permintaan tidak menjadi
	// panic saat diiris.
	if cursor.Pos >= total {
		return ResolvePage{
			Found:      []model.Outfit{},
			NotFound:   []string{},
			Total:      total,
			TotalPages: totalPages,
		}, nil
	}

	end := min(cursor.Pos+limit, total)
	page := referenceIDs[cursor.Pos:end]

	found, err := s.outfits.ListByReferenceIDs(ctx, page)
	if err != nil {
		return ResolvePage{}, err
	}

	seen := make(map[string]struct{}, len(found))
	for _, o := range found {
		seen[o.ReferenceID] = struct{}{}
	}

	notFound := make([]string, 0)
	for _, ref := range page {
		if _, ok := seen[ref]; !ok {
			notFound = append(notFound, ref)
		}
	}

	result := ResolvePage{
		Found:      found,
		NotFound:   notFound,
		HasMore:    end < total,
		Total:      total,
		TotalPages: totalPages,
	}
	if result.HasMore {
		result.NextCursor, err = paging.Encode(paging.PositionCursor{Pos: end})
		if err != nil {
			return ResolvePage{}, err
		}
	}
	return result, nil
}

// load mengambil outfit dan menerjemahkan keadaannya menjadi 404 atau 410.
// Engagement adalah hasil pencatatan like atau view, dikembalikan supaya klien
// bisa langsung memperbarui tampilannya tanpa GET susulan.
type Engagement struct {
	OutfitID string
	// Changed melaporkan apakah permintaan ini benar-benar mengubah sesuatu.
	// false pada like berulang dari pemain yang sama — bukan error, karena
	// tombol suka yang ditekan dua kali harus berakhir pada keadaan yang sama.
	Changed   bool
	Liked     bool
	LikeCount int
	ViewCount int
}

// Like mencatat bahwa pemanggil menyukai sebuah outfit.
//
// Aktor wajib ada: like tanpa identitas tidak bisa dibatalkan, tidak bisa
// dijaga tetap satu per pemain, dan tidak berguna sebagai data latih karena
// justru pasangan (pemain, outfit) yang jadi sinyalnya.
func (s *Outfits) Like(ctx context.Context, actor Actor, outfitID string) (Engagement, error) {
	outfit, err := s.requireEngagementTarget(ctx, actor, outfitID)
	if err != nil {
		return Engagement{}, err
	}

	counts, err := s.outfits.Like(ctx, outfit.OutfitID, actor.UserID, s.now())
	if err != nil {
		return Engagement{}, mapEngagementErr(err, outfitID)
	}
	return newEngagement(outfit.OutfitID, counts, true), nil
}

// Unlike membatalkan like pemanggil. Idempoten seperti Like.
func (s *Outfits) Unlike(ctx context.Context, actor Actor, outfitID string) (Engagement, error) {
	outfit, err := s.requireEngagementTarget(ctx, actor, outfitID)
	if err != nil {
		return Engagement{}, err
	}

	counts, err := s.outfits.Unlike(ctx, outfit.OutfitID, actor.UserID)
	if err != nil {
		return Engagement{}, mapEngagementErr(err, outfitID)
	}
	return newEngagement(outfit.OutfitID, counts, false), nil
}

// RecordView mencatat satu kejadian lihat.
//
// Berbeda dengan Like, aktor boleh kosong: view anonim masih berarti untuk
// popularitas walau tidak terpakai sebagai sinyal per pemain. Tiap panggilan
// menambah satu baris — klien yang memanggilnya dua kali memang melaporkan dua
// kali dilihat.
func (s *Outfits) RecordView(ctx context.Context, actor Actor, outfitID string) (Engagement, error) {
	outfit, err := s.load(ctx, outfitID)
	if err != nil {
		return Engagement{}, err
	}

	counts, err := s.outfits.RecordView(ctx, outfit.OutfitID, actor.UserID, s.now())
	if err != nil {
		return Engagement{}, mapEngagementErr(err, outfitID)
	}
	return newEngagement(outfit.OutfitID, counts, false), nil
}

// newEngagement menyusun balasan dari angka yang dikembalikan penyimpanan.
//
// Angkanya sengaja TIDAK dihitung dari outfit yang barusan dibaca lalu ditambah
// satu: pembacaan itu bisa dilayani cache yang tertinggal, sehingga balasan
// atas aksi yang baru saja dilakukan klien justru melaporkan angka lama.
func newEngagement(outfitID string, counts store.EngagementCounts, liked bool) Engagement {
	return Engagement{
		OutfitID:  outfitID,
		Changed:   counts.Changed,
		Liked:     liked,
		LikeCount: counts.LikeCount,
		ViewCount: counts.ViewCount,
	}
}

// LikedBy melaporkan outfit mana saja dari daftar yang sudah disukai aktor.
// Aktor anonim mendapat peta kosong, bukan error: daftar tetap bisa dibaca
// tanpa login, hanya tanpa penanda "sudah kamu suka".
func (s *Outfits) LikedBy(ctx context.Context, actor Actor, outfits []model.Outfit) (map[string]bool, error) {
	if !actor.Present || len(outfits) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(outfits))
	for _, o := range outfits {
		ids = append(ids, o.OutfitID)
	}
	return s.outfits.Liked(ctx, actor.UserID, ids)
}

// requireEngagementTarget memastikan outfit ada, hidup, dan pemanggilnya
// dikenali.
func (s *Outfits) requireEngagementTarget(ctx context.Context, actor Actor, outfitID string) (model.Outfit, error) {
	if !actor.Present {
		return model.Outfit{}, apierr.Unauthorized("actor_required",
			"Menyukai outfit butuh identitas pemain")
	}
	return s.load(ctx, outfitID)
}

// mapEngagementErr menerjemahkan balapan antara pembacaan dan penulisan: outfit
// yang lolos load bisa saja sudah dihapus saat penulisan berjalan.
func mapEngagementErr(err error, outfitID string) error {
	if errors.Is(err, store.ErrNotFound) {
		return apierr.NotFound("outfit_not_found", fmt.Sprintf("Outfit %s tidak ditemukan", outfitID))
	}
	return err
}

func (s *Outfits) load(ctx context.Context, outfitID string) (model.Outfit, error) {
	if strings.TrimSpace(outfitID) == "" {
		return model.Outfit{}, apierr.NotFound("outfit_not_found", "Outfit tidak ditemukan")
	}

	outfit, err := s.outfits.Get(ctx, outfitID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Outfit{}, apierr.NotFound("outfit_not_found", fmt.Sprintf("Outfit %s tidak ditemukan", outfitID))
	}
	if err != nil {
		return model.Outfit{}, err
	}
	if outfit.Deleted() {
		return model.Outfit{}, apierr.Gone("outfit_deleted", fmt.Sprintf("Outfit dihapus pada %s", outfit.DeletedAt.Format(time.RFC3339)))
	}
	return outfit, nil
}

// ensureTemplate memastikan rig terdaftar di BODY_TEMPLATE, mendaftarkannya
// bila ini pemakaian pertama.
//
// Rig di-upload ke Roblox lebih dulu dan Roblox yang memegang asetnya, jadi
// menolak asset id yang belum pernah dilihat backend hanya akan menghalangi
// alur yang sah. Yang tetap ditolak adalah id yang bukan asset id sama sekali,
// supaya registry tidak terisi salah ketik dan FK OUTFIT.templateId tetap berarti.
func (s *Outfits) ensureTemplate(ctx context.Context, templateID string) error {
	assetID, err := ParseTemplateID(templateID)
	if err != nil {
		return err
	}
	templateID = strings.TrimSpace(templateID)

	if _, err := s.templates.Get(ctx, templateID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	// Nama dan gender dibiarkan kosong — backend tidak tahu keduanya sampai
	// diisi lewat PATCH /v1/templates/{templateId}.
	_, _, err = s.templates.Ensure(ctx, model.BodyTemplate{
		TemplateID:    templateID,
		Gender:        genderUnknown,
		SourceAssetID: assetID,
		CreatedAt:     s.now(),
	})
	return err
}

// validateItems memeriksa jumlah dan bentuk detail item.
//
// Yang tidak diperiksa: apakah assetId-nya benar-benar ada di Roblox. Backend
// tidak lagi menyimpan katalog, jadi satu-satunya yang tahu itu adalah klien —
// dan menebaknya di sini hanya akan menolak item yang sah.
//
// Slot ganda juga tidak ditolak. Klien yang tahu apakah dua asset benar-benar
// bentrok di rig; slot di sini cuma label yang dilaporkan klien.
func (s *Outfits) validateItems(items []model.OutfitItem) error {
	if len(items) > MaxOutfitItems {
		return apierr.Unprocessable("too_many_items", fmt.Sprintf("Maksimum %d item per outfit", MaxOutfitItems))
	}

	for _, item := range items {
		if strings.TrimSpace(item.Slot) == "" {
			return apierr.Unprocessable("missing_slot", "Setiap item wajib punya slot")
		}
		if item.AssetID <= 0 {
			return apierr.Unprocessable("invalid_asset_id", "Setiap item wajib punya assetId")
		}

		if len(strings.TrimSpace(item.Name)) > MaxItemNameLen {
			return apierr.Unprocessable("invalid_item_name",
				fmt.Sprintf("name item maksimum %d karakter", MaxItemNameLen))
		}
		if len(strings.TrimSpace(item.AssetType)) > MaxAssetTypeLen {
			return apierr.Unprocessable("invalid_asset_type",
				fmt.Sprintf("assetType maksimum %d karakter", MaxAssetTypeLen))
		}
		if item.Price < 0 {
			return apierr.Unprocessable("invalid_item_price", "price item tidak boleh negatif")
		}

		// bundleId hanya divalidasi bentuknya; apakah bundle-nya benar ada di
		// Roblox mengikuti aturan yang sama dengan assetId — klien yang tahu.
		if item.BundleID < 0 {
			return apierr.Unprocessable("invalid_bundle_id", "bundleId tidak boleh negatif")
		}
		if item.BundleID == 0 && strings.TrimSpace(item.BundleName) != "" {
			return apierr.Unprocessable("invalid_bundle_name", "bundleName butuh bundleId")
		}
		if len(strings.TrimSpace(item.BundleName)) > MaxItemNameLen {
			return apierr.Unprocessable("invalid_bundle_name",
				fmt.Sprintf("bundleName maksimum %d karakter", MaxItemNameLen))
		}
	}
	return nil
}

// normalizeBody memeriksa warna dan skala tubuh, lalu mengembalikan salinan
// yang sudah dirapikan.
//
// Yang tidak diperiksa: apakah skalanya masuk rentang yang diterima Roblox.
// Rentang itu bergeser antar rig dan antar rilis, jadi menebaknya di sini hanya
// akan menolak avatar yang sah — sama alasannya dengan assetId item. Yang
// ditolak hanya nilai yang tidak mungkin benar dan pasti bikin render gagal.
func normalizeBody(body *model.AvatarBody) (*model.AvatarBody, error) {
	if body == nil {
		return nil, nil
	}

	out := model.AvatarBody{}

	if body.Colors != nil {
		colors := *body.Colors
		parts := []struct {
			field string
			value *string
		}{
			{"head", &colors.Head},
			{"torso", &colors.Torso},
			{"leftArm", &colors.LeftArm},
			{"rightArm", &colors.RightArm},
			{"leftLeg", &colors.LeftLeg},
			{"rightLeg", &colors.RightLeg},
		}
		for _, part := range parts {
			normalized, err := normalizeHexColor(*part.value)
			if err != nil {
				return nil, apierr.Unprocessable("invalid_body_color",
					fmt.Sprintf("body.colors.%s harus hex RGB 6 digit", part.field)).
					WithDetails(map[string]any{
						"field": "body.colors." + part.field,
						"value": *part.value,
					})
			}
			*part.value = normalized
		}
		out.Colors = &colors
	}

	if body.Scales != nil {
		scales := *body.Scales
		// Height/width/head/depth adalah pengali: nol berarti anggota badan
		// menghilang, dan itu selalu salah kirim — biasanya karena field-nya
		// lupa diisi, bukan karena benar-benar diinginkan.
		positive := []struct {
			field string
			value float64
		}{
			{"height", scales.Height},
			{"width", scales.Width},
			{"head", scales.Head},
			{"depth", scales.Depth},
		}
		for _, s := range positive {
			if s.value <= 0 {
				return nil, apierr.Unprocessable("invalid_body_scale",
					fmt.Sprintf("body.scales.%s harus lebih besar dari nol", s.field)).
					WithDetails(map[string]any{"field": "body.scales." + s.field, "value": s.value})
			}
		}
		// BodyType dan proportion adalah bobot; nol adalah nilai wajarnya.
		weights := []struct {
			field string
			value float64
		}{
			{"bodyType", scales.BodyType},
			{"proportion", scales.Proportion},
		}
		for _, s := range weights {
			if s.value < 0 {
				return nil, apierr.Unprocessable("invalid_body_scale",
					fmt.Sprintf("body.scales.%s tidak boleh negatif", s.field)).
					WithDetails(map[string]any{"field": "body.scales." + s.field, "value": s.value})
			}
		}
		out.Scales = &scales
	}

	// body: {} tidak membawa informasi apa pun; simpan sebagai tidak dilaporkan
	// supaya tidak ada dua bentuk berbeda untuk keadaan yang sama.
	if out.Colors == nil && out.Scales == nil {
		return nil, nil
	}
	return &out, nil
}

// normalizeHexColor menerima "AE7C64" maupun "#AE7C64" dan mengembalikannya
// tanpa '#'. Besar-kecil huruf dibiarkan seperti yang dikirim klien.
func normalizeHexColor(value string) (string, error) {
	hex := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(hex) != 6 {
		return "", errInvalidHex
	}
	for _, r := range hex {
		isDigit := r >= '0' && r <= '9'
		isHexLetter := (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isDigit && !isHexLetter {
			return "", errInvalidHex
		}
	}
	return hex, nil
}

var errInvalidHex = errors.New("bukan hex RGB 6 digit")

// validateCustomTags menolak tag yang mengandung koma, karena tag dikirim ke
// RegisterItemAsync sebagai satu string dipisah koma dan akan terpecah salah.
func validateCustomTags(tags []string) error {
	if len(tags) > MaxCustomTags {
		return apierr.Unprocessable("too_many_custom_tags", fmt.Sprintf("Maksimum %d customTags", MaxCustomTags))
	}

	invalid := apierr.Unprocessable("invalid_custom_tag", "CustomTags tidak boleh mengandung koma")
	for i, tag := range tags {
		if strings.Contains(tag, ",") {
			invalid.WithDetails(map[string]any{
				"field": fmt.Sprintf("customTags[%d]", i),
				"value": tag,
			})
		}
	}
	if len(invalid.Details) > 0 {
		return invalid
	}
	return nil
}

// ensureOwner menolak perubahan dari pemain lain. Selama autentikasi belum
// terpasang, actor tanpa identitas dilewatkan begitu saja.
func ensureOwner(actor Actor, outfit model.Outfit) error {
	if !actor.Present || actor.UserID == outfit.UserID {
		return nil
	}
	return apierr.Forbidden("not_owner", fmt.Sprintf("userId %d bukan pemilik outfit %s", actor.UserID, outfit.OutfitID))
}
