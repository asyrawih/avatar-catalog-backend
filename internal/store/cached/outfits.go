package cached

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/cache"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Outfits membungkus store.Outfits dengan cache baca.
//
// Semua entri — detail maupun daftar, milik pemain mana pun — hidup di bawah
// SATU versi global: setiap penulisan outfit menaikkan versi itu sehingga
// seluruh cache outfit gugur sekaligus. Kasar, tapi sederhana dan tidak
// mungkin menyajikan data basi; TTL pendek menjaga entri lama tidak menumpuk.
// Kunci idempotensi di Redis yang sama tidak tersentuh.
type Outfits struct {
	inner  store.Outfits
	cache  cache.Cache
	ttl    time.Duration
	logger *slog.Logger
}

// NewOutfits membungkus penyimpanan outfit dengan cache. ttl <= 0 memakai 60 detik.
func NewOutfits(inner store.Outfits, c cache.Cache, ttl time.Duration, logger *slog.Logger) *Outfits {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Outfits{inner: inner, cache: c, ttl: ttl, logger: logger}
}

var _ store.Outfits = (*Outfits)(nil)

// Create meneruskan penulisan lalu membatalkan seluruh cache outfit.
func (s *Outfits) Create(ctx context.Context, o model.Outfit) error {
	if err := s.inner.Create(ctx, o); err != nil {
		return err
	}
	s.invalidateAll(ctx)
	return nil
}

// Get mengembalikan outfit, lewat cache bila tersedia.
func (s *Outfits) Get(ctx context.Context, outfitID string) (model.Outfit, error) {
	key := s.versionedKey(ctx, "row", outfitID)

	var cachedOutfit model.Outfit
	found, err := s.cache.Get(ctx, key, &cachedOutfit)
	if err != nil {
		s.logger.Warn("gagal membaca cache outfit", "outfitId", outfitID, "err", err)
	}
	if found {
		return cachedOutfit, nil
	}

	outfit, err := s.inner.Get(ctx, outfitID)
	if err != nil {
		return model.Outfit{}, err
	}
	s.set(ctx, key, outfit)
	return outfit, nil
}

// outfitPage adalah bentuk yang disimpan di cache untuk satu halaman daftar.
type outfitPage struct {
	Outfits []model.Outfit `json:"outfits"`
	HasMore bool           `json:"hasMore"`
}

// List mengembalikan daftar outfit, lewat cache bila tersedia. Semua varian
// daftar dijaga versi global yang sama.
func (s *Outfits) List(ctx context.Context, f store.OutfitFilter, after *paging.KeysetCursor, limit int) ([]model.Outfit, bool, error) {
	cursorKey := "first"
	if after != nil {
		cursorKey = cache.HashString(after.At.UTC().Format(time.RFC3339Nano) + "|" + after.ID)
	}

	publicKey := "all"
	if f.IsPublic != nil {
		publicKey = strconv.FormatBool(*f.IsPublic)
	}

	// Pencarian ikut menentukan isi halaman, jadi keyword dan daftar id harus
	// masuk kunci — tanpa itu hasil satu pencarian akan disajikan untuk
	// pencarian lain yang parameter lainnya kebetulan sama.
	searchKey := "all"
	if f.Keyword != "" || len(f.OutfitIDs) > 0 {
		ids := append([]string(nil), f.OutfitIDs...)
		sort.Strings(ids)
		searchKey = cache.HashString(f.Keyword + "|" + strings.Join(ids, ","))
	}
	key := s.versionedKey(ctx, "list", strconv.FormatInt(f.UserID, 10),
		publicKey, searchKey, cursorKey, strconv.Itoa(limit))

	var page outfitPage
	found, err := s.cache.Get(ctx, key, &page)
	if err != nil {
		s.logger.Warn("gagal membaca cache daftar outfit", "userId", f.UserID, "err", err)
	}
	if found {
		return page.Outfits, page.HasMore, nil
	}

	outfits, hasMore, err := s.inner.List(ctx, f, after, limit)
	if err != nil {
		return nil, false, err
	}
	s.set(ctx, key, outfitPage{Outfits: outfits, HasMore: hasMore})
	return outfits, hasMore, nil
}

// Search mengembalikan hasil pencarian, lewat cache bila tersedia. Kuncinya
// cukup keyword + filter + limit: qEmbedding diturunkan dari keyword yang sama,
// jadi tidak menambah pembeda.
func (s *Outfits) Search(ctx context.Context, f store.OutfitFilter, qEmbedding []float32, limit int) ([]model.Outfit, error) {
	publicKey := "all"
	if f.IsPublic != nil {
		publicKey = strconv.FormatBool(*f.IsPublic)
	}
	key := s.versionedKey(ctx, "search", strconv.FormatInt(f.UserID, 10),
		publicKey, cache.HashString(f.Keyword), strconv.Itoa(limit))

	var cachedRows []model.Outfit
	found, err := s.cache.Get(ctx, key, &cachedRows)
	if err != nil {
		s.logger.Warn("gagal membaca cache pencarian outfit", "err", err)
	}
	if found {
		return cachedRows, nil
	}

	rows, err := s.inner.Search(ctx, f, qEmbedding, limit)
	if err != nil {
		return nil, err
	}
	s.set(ctx, key, rows)
	return rows, nil
}

// SetNameEmbedding meneruskan penulisan lalu membatalkan seluruh cache outfit:
// embedding baru bisa mengubah peringkat hasil pencarian yang sudah ter-cache.
func (s *Outfits) SetNameEmbedding(ctx context.Context, outfitID string, embedding []float32) error {
	if err := s.inner.SetNameEmbedding(ctx, outfitID, embedding); err != nil {
		return err
	}
	s.invalidateAll(ctx)
	return nil
}

// ListByReferenceIDs diteruskan tanpa cache.
//
// Feed rekomendasi mengirim kombinasi referenceId yang hampir selalu berbeda,
// jadi entri cache-nya nyaris tidak pernah terpakai ulang dan hanya membebani
// Redis.
func (s *Outfits) ListByReferenceIDs(ctx context.Context, referenceIDs []string) ([]model.Outfit, error) {
	return s.inner.ListByReferenceIDs(ctx, referenceIDs)
}

// Update meneruskan penulisan lalu membatalkan seluruh cache outfit.
func (s *Outfits) Update(ctx context.Context, outfitID string, fn func(*model.Outfit) error) (model.Outfit, error) {
	updated, err := s.inner.Update(ctx, outfitID, fn)
	if err != nil {
		return model.Outfit{}, err
	}
	s.invalidateAll(ctx)
	return updated, nil
}

func (s *Outfits) set(ctx context.Context, key string, value any) {
	if err := s.cache.Set(ctx, key, value, s.ttl); err != nil {
		s.logger.Warn("gagal menulis cache outfit", "key", key, "err", err)
	}
}

// versionedKey merangkai kunci cache di bawah versi global berjalan. Kegagalan
// membaca versi hanya di-log: kunci jatuh ke v0 dan paling buruk jadi miss.
func (s *Outfits) versionedKey(ctx context.Context, parts ...string) string {
	version, err := s.cache.Version(ctx, globalNamespace)
	if err != nil {
		s.logger.Warn("gagal membaca versi cache outfit", "err", err)
	}
	return cache.Key(append([]string{globalNamespace, "v" + strconv.FormatInt(version, 10)}, parts...)...)
}

// invalidateAll menaikkan versi global sehingga seluruh entri outfit — detail
// maupun daftar semua pemain — tidak terbaca lagi. Entri lama hilang sendiri
// saat TTL habis; kunci lain di Redis (mis. idempotensi) tidak tersentuh.
func (s *Outfits) invalidateAll(ctx context.Context) {
	if err := s.cache.BumpVersion(ctx, globalNamespace); err != nil {
		s.logger.Error("gagal membatalkan cache outfit", "err", err)
	}
}

// globalNamespace menaungi seluruh cache outfit.
const globalNamespace = "outfit:all"
