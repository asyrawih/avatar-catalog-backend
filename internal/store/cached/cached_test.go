package cached_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/cache"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
	"github.com/hanan/avatar-catalog-backend/internal/store/cached"
)

const (
	assetHair  = int64(78872304386489)
	assetDead  = int64(91837981092769)
	seedOutfit = "otf_9f2a41"
	seedUser   = int64(627278822)
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// --- cache palsu ------------------------------------------------------------

// fakeCache adalah Cache in-memory yang mencatat pemakaiannya, dan bisa
// dipaksa gagal untuk menguji bahwa kegagalan cache tidak menjatuhkan request.
type fakeCache struct {
	mu       sync.Mutex
	entries  map[string][]byte
	versions map[string]int64
	failing  bool

	hits, misses, sets, deletes int
}

func newFakeCache() *fakeCache {
	return &fakeCache{entries: map[string][]byte{}, versions: map[string]int64{}}
}

var _ cache.Cache = (*fakeCache)(nil)

var errCacheDown = errors.New("cache mati")

func (f *fakeCache) Get(_ context.Context, key string, dst any) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failing {
		return false, errCacheDown
	}
	raw, ok := f.entries[key]
	if !ok {
		f.misses++
		return false, nil
	}
	f.hits++
	return true, json.Unmarshal(raw, dst)
}

func (f *fakeCache) Set(_ context.Context, key string, value any, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failing {
		return errCacheDown
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f.sets++
	f.entries[key] = raw
	return nil
}

func (f *fakeCache) Delete(_ context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failing {
		return errCacheDown
	}
	for _, key := range keys {
		delete(f.entries, key)
		f.deletes++
	}
	return nil
}

func (f *fakeCache) Version(_ context.Context, namespace string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failing {
		return 0, errCacheDown
	}
	return f.versions[namespace], nil
}

func (f *fakeCache) BumpVersion(_ context.Context, namespace string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failing {
		return errCacheDown
	}
	f.versions[namespace]++
	return nil
}

func (f *fakeCache) Ping(context.Context) error { return nil }
func (f *fakeCache) Close() error               { return nil }

func (f *fakeCache) breakIt() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = true
}

// --- store penghitung -------------------------------------------------------

type countingOutfits struct {
	store.Outfits
	gets  int
	lists int
}

func (c *countingOutfits) Get(ctx context.Context, outfitID string) (model.Outfit, error) {
	c.gets++
	return c.Outfits.Get(ctx, outfitID)
}

func (c *countingOutfits) List(ctx context.Context, f store.OutfitFilter, after *paging.KeysetCursor, limit int) ([]model.Outfit, bool, error) {
	c.lists++
	return c.Outfits.List(ctx, f, after, limit)
}

func newBackends(t *testing.T) *countingOutfits {
	t.Helper()

	templates := store.NewMemoryTemplates()
	outfits := store.NewMemoryOutfits()
	store.SeedData(templates, outfits)

	return &countingOutfits{Outfits: outfits}
}

func TestOutfitGetDilayaniCacheDanDibuangSaatUpdate(t *testing.T) {
	inner := newBackends(t)
	c := newFakeCache()
	svc := cached.NewOutfits(inner, c, time.Minute, discardLogger())
	ctx := context.Background()

	if _, err := svc.Get(ctx, seedOutfit); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, err := svc.Get(ctx, seedOutfit); err != nil {
		t.Fatalf("Get() kedua error = %v", err)
	}
	if inner.gets != 1 {
		t.Fatalf("penyimpanan dipukul %d kali, ingin 1", inner.gets)
	}

	if _, err := svc.Update(ctx, seedOutfit, func(o *model.Outfit) error {
		o.Name = "Nama Baru"
		o.UpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	updated, err := svc.Get(ctx, seedOutfit)
	if err != nil {
		t.Fatalf("Get() setelah update error = %v", err)
	}
	if inner.gets != 2 {
		t.Errorf("penyimpanan dipukul %d kali, ingin 2 setelah cache dibuang", inner.gets)
	}
	if updated.Name != "Nama Baru" {
		t.Errorf("name = %q, ingin nama hasil update — cache basi terbaca", updated.Name)
	}
}

func TestOutfitListDibatalkanPerPemain(t *testing.T) {
	inner := newBackends(t)
	c := newFakeCache()
	svc := cached.NewOutfits(inner, c, time.Minute, discardLogger())
	ctx := context.Background()

	if _, _, err := svc.List(ctx, store.OutfitFilter{UserID: seedUser}, nil, 20); err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if _, _, err := svc.List(ctx, store.OutfitFilter{UserID: seedUser}, nil, 20); err != nil {
		t.Fatalf("ListByUser() kedua error = %v", err)
	}
	if inner.lists != 1 {
		t.Fatalf("penyimpanan dipukul %d kali, ingin 1", inner.lists)
	}

	// Daftar lintas pemain dijaga versi global, bukan versi pemain.
	if _, _, err := svc.List(ctx, store.OutfitFilter{}, nil, 20); err != nil {
		t.Fatalf("List() global error = %v", err)
	}
	if _, _, err := svc.List(ctx, store.OutfitFilter{}, nil, 20); err != nil {
		t.Fatalf("List() global kedua error = %v", err)
	}
	if inner.lists != 2 {
		t.Fatalf("penyimpanan dipukul %d kali, ingin 2 (per pemain + global, masing-masing sekali)", inner.lists)
	}

	// Membuat outfit untuk pemain lain tidak boleh membuang cache pemain ini.
	if err := svc.Create(ctx, model.Outfit{
		OutfitID: "otf_lain01", ReferenceID: "11111111-1111-4111-8111-111111111111",
		UserID: 999, TemplateID: "88484288792766", Name: "Punya Orang Lain",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, _, err := svc.List(ctx, store.OutfitFilter{UserID: seedUser}, nil, 20); err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if inner.lists != 2 {
		t.Errorf("penyimpanan dipukul %d kali, ingin tetap 2 (cache pemain lain ikut terbuang)", inner.lists)
	}

	// Sebaliknya, daftar global HARUS gugur karena outfit pemain lain masuk ke sana.
	if _, _, err := svc.List(ctx, store.OutfitFilter{}, nil, 20); err != nil {
		t.Fatalf("List() global error = %v", err)
	}
	if inner.lists != 3 {
		t.Errorf("penyimpanan dipukul %d kali, ingin 3 (daftar global harus dibaca ulang)", inner.lists)
	}

	// Membuat outfit untuk pemain yang sama harus membuang cachenya.
	if err := svc.Create(ctx, model.Outfit{
		OutfitID: "otf_baru01", ReferenceID: "22222222-2222-4222-8222-222222222222",
		UserID: seedUser, TemplateID: "88484288792766", Name: "Punya Sendiri",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rows, _, err := svc.List(ctx, store.OutfitFilter{UserID: seedUser}, nil, 20)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if inner.lists != 4 {
		t.Errorf("penyimpanan dipukul %d kali, ingin 4 setelah outfit baru", inner.lists)
	}
	if len(rows) != 3 {
		t.Errorf("jumlah outfit = %d, ingin 3 — daftar basi terbaca", len(rows))
	}
}
