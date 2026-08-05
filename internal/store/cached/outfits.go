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
// Detail outfit dibatalkan per baris, sedangkan daftar per pemain dibatalkan
// lewat versi namespace miliknya sendiri — perubahan pada satu pemain tidak
// membuang cache pemain lain.
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

// Create meneruskan penulisan lalu membatalkan daftar milik pemain itu.
func (s *Outfits) Create(ctx context.Context, o model.Outfit) error {
	if err := s.inner.Create(ctx, o); err != nil {
		return err
	}
	s.invalidateUser(ctx, o.UserID)
	return nil
}

// Get mengembalikan outfit, lewat cache bila tersedia.
func (s *Outfits) Get(ctx context.Context, outfitID string) (model.Outfit, error) {
	key := outfitKey(outfitID)

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

// List mengembalikan daftar outfit, lewat cache bila tersedia.
//
// Daftar per pemain dibatalkan lewat versi milik pemain itu, sedangkan daftar
// lintas pemain memakai versi global yang naik pada setiap penulisan — daftar
// gabungan tidak bisa dijaga oleh versi satu pemain saja.
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
	key := s.listKey(ctx, f.UserID, publicKey, searchKey, cursorKey, strconv.Itoa(limit))

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

// ListByReferenceIDs diteruskan tanpa cache.
//
// Feed rekomendasi mengirim kombinasi referenceId yang hampir selalu berbeda,
// jadi entri cache-nya nyaris tidak pernah terpakai ulang dan hanya membebani
// Redis.
func (s *Outfits) ListByReferenceIDs(ctx context.Context, referenceIDs []string) ([]model.Outfit, error) {
	return s.inner.ListByReferenceIDs(ctx, referenceIDs)
}

// Update meneruskan penulisan lalu membuang cache baris dan daftar terkait.
func (s *Outfits) Update(ctx context.Context, outfitID string, fn func(*model.Outfit) error) (model.Outfit, error) {
	updated, err := s.inner.Update(ctx, outfitID, fn)
	if err != nil {
		return model.Outfit{}, err
	}

	if err := s.cache.Delete(ctx, outfitKey(outfitID)); err != nil {
		s.logger.Error("gagal membuang cache outfit", "outfitId", outfitID, "err", err)
	}
	s.invalidateUser(ctx, updated.UserID)
	return updated, nil
}

func (s *Outfits) set(ctx context.Context, key string, value any) {
	if err := s.cache.Set(ctx, key, value, s.ttl); err != nil {
		s.logger.Warn("gagal menulis cache outfit", "key", key, "err", err)
	}
}

// listKey merangkai kunci daftar beserta versi namespace yang menjaganya.
func (s *Outfits) listKey(ctx context.Context, userID int64, parts ...string) string {
	namespace := listNamespace(userID)

	version, err := s.cache.Version(ctx, namespace)
	if err != nil {
		s.logger.Warn("gagal membaca versi cache outfit", "userId", userID, "err", err)
	}
	return cache.Key(append([]string{namespace, "v" + strconv.FormatInt(version, 10)}, parts...)...)
}

// invalidateUser menaikkan versi pemain yang berubah sekaligus versi global,
// karena outfit itu juga muncul di daftar lintas pemain.
func (s *Outfits) invalidateUser(ctx context.Context, userID int64) {
	for _, namespace := range []string{listNamespace(userID), globalNamespace} {
		if err := s.cache.BumpVersion(ctx, namespace); err != nil {
			s.logger.Error("gagal membatalkan cache daftar outfit", "namespace", namespace, "err", err)
		}
	}
}

func outfitKey(outfitID string) string { return cache.Key("outfit", outfitID) }

// globalNamespace menjaga daftar outfit lintas pemain.
const globalNamespace = "outfit:all"

func listNamespace(userID int64) string {
	if userID == 0 {
		return globalNamespace
	}
	return cache.Key("outfit", "user", strconv.FormatInt(userID, 10))
}
