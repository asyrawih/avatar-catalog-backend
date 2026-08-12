package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
	"github.com/hanan/avatar-catalog-backend/internal/store/postgres"
)

// Test di file ini butuh Postgres sungguhan yang skemanya sudah dimuat dari
// db/init. Jalankan dengan:
//
//	docker compose up -d db
//	TEST_DATABASE_URL=postgres://avatar:avatar_dev_password@localhost:5432/avatar_catalog?sslmode=disable go test ./internal/store/postgres/...
//
// PERINGATAN: setiap test mengosongkan tabel, jadi arahkan hanya ke database
// sekali pakai.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL tidak diisi; melewati test integrasi Postgres")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.Open(ctx, dsn, postgres.PoolConfig{MaxConns: 4, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	resetSchema(t, pool)
	return pool
}

// resetSchema mengosongkan tabel lalu memasang data minimal yang dibutuhkan
// foreign key.
func resetSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		TRUNCATE api_key, outfit_like, outfit_view,
		         transaction_item, transaction, outfit_item, outfit, body_template, player
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate error = %v", err)
	}

	_, err = pool.Exec(ctx, `
		-- created_at diisi eksplisit, bukan dibiarkan DEFAULT now(). Test yang
		-- menguji urutan daftar rig menyisipkan barisnya dengan time.Now() dari
		-- Go, dan membandingkan jam host dengan jam server Postgres membuat
		-- urutannya bergantung pada selisih kedua jam itu.
		INSERT INTO body_template (template_id, name, gender, source_asset_id, created_at)
		VALUES ('88484288792766', 'Dev Rig', '?', 88484288792766, '2026-08-04T00:00:00Z');`)
	if err != nil {
		t.Fatalf("seed error = %v", err)
	}
}

func sampleOutfit(id, referenceID string, updatedAt time.Time) model.Outfit {
	return model.Outfit{
		OutfitID:    id,
		ReferenceID: referenceID,
		UserID:      627278822,
		TemplateID:  "88484288792766",
		Name:        "Y2K Streetwear",
		IsPublic:    true,
		CustomTags:  []string{"category:y2k", "gender:male"},
		Items: []model.OutfitItem{
			{AssetID: 78872304386489, Slot: "Hair", Name: "BLOND BARREL TWISTS DREADS", AssetType: "HairAccessory", Price: 69},
			{AssetID: 14433369343, Slot: "Jacket", Name: "Hero Jacket Oni Blood Moon", AssetType: "Accessory", Price: 79},
		},
		Body: &model.AvatarBody{
			Colors: &model.BodyColors{
				Head: "AE7C64", Torso: "AE7C64",
				LeftArm: "AE7C64", RightArm: "AE7C64",
				LeftLeg: "AE7C64", RightLeg: "AE7C64",
			},
			Scales: &model.BodyScales{
				Height: 1.0499999523162842, Width: 1, Head: 1,
				Depth: 1, BodyType: 1, Proportion: 0,
			},
		},
		CreatedAt: updatedAt,
		UpdatedAt: updatedAt,
	}
}

func TestOutfitTulisLaluBaca(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	// Baris PLAYER belum ada; adapter yang wajib memenuhi foreign key-nya.
	want := sampleOutfit("otf_test01", "550e8400-e29b-41d4-a716-446655440000", time.Now().UTC().Truncate(time.Microsecond))
	if err := outfits.Create(ctx, want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := outfits.Get(ctx, want.OutfitID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.ReferenceID != want.ReferenceID {
		t.Errorf("referenceId = %q, ingin %q", got.ReferenceID, want.ReferenceID)
	}
	if len(got.CustomTags) != 2 || got.CustomTags[0] != "category:y2k" {
		t.Errorf("customTags = %v", got.CustomTags)
	}
	if len(got.Items) != 2 {
		t.Fatalf("jumlah item = %d, ingin 2", len(got.Items))
	}
	// Detail item disimpan di OUTFIT_ITEM, jadi harus ikut terbaca kembali.
	if item := got.Items[0]; item.Name != "BLOND BARREL TWISTS DREADS" ||
		item.AssetType != "HairAccessory" || item.Price != 69 {
		t.Errorf("item = %+v, ingin detail ikut tersimpan", item)
	}
	if got.RecoItemID != nil {
		t.Errorf("recoItemId = %v, ingin nil", *got.RecoItemID)
	}
	if got.Deleted() {
		t.Error("outfit baru sudah bertanda terhapus")
	}

	// OUTFIT.body adalah jsonb; yang diuji di sini bahwa isinya kembali utuh,
	// termasuk pecahan skala yang tidak boleh dibulatkan.
	if got.Body == nil || got.Body.Colors == nil || got.Body.Scales == nil {
		t.Fatalf("body = %+v, ingin colors dan scales ikut terbaca", got.Body)
	}
	if got.Body.Colors.Head != "AE7C64" || got.Body.Colors.RightLeg != "AE7C64" {
		t.Errorf("body.colors = %+v", *got.Body.Colors)
	}
	if got.Body.Scales.Height != want.Body.Scales.Height {
		t.Errorf("body.scales.height = %v, ingin %v", got.Body.Scales.Height, want.Body.Scales.Height)
	}
	if got.Body.Scales.Proportion != 0 || got.Body.Scales.BodyType != 1 {
		t.Errorf("body.scales = %+v", *got.Body.Scales)
	}
}

// Outfit tanpa body harus tersimpan sebagai NULL dan terbaca kembali sebagai
// nil — bukan objek berisi nol yang terbaca seperti data sungguhan.
func TestOutfitTanpaBody(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	want := sampleOutfit("otf_test04", "550e8400-e29b-41d4-a716-446655440003", time.Now().UTC().Truncate(time.Microsecond))
	want.Body = nil
	if err := outfits.Create(ctx, want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := outfits.Get(ctx, want.OutfitID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Body != nil {
		t.Errorf("body = %+v, ingin nil", got.Body)
	}
}

func TestOutfitUpdateDanSoftDelete(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	base := sampleOutfit("otf_test02", "550e8400-e29b-41d4-a716-446655440001", time.Now().UTC().Truncate(time.Microsecond))
	if err := outfits.Create(ctx, base); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	recoItemID := "reco_7b31c9"
	updated, err := outfits.Update(ctx, base.OutfitID, func(o *model.Outfit) error {
		o.RecoItemID = &recoItemID
		o.Items = []model.OutfitItem{{AssetID: 78872304386489, Slot: "Hair"}}
		o.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.RecoItemID == nil || *updated.RecoItemID != recoItemID {
		t.Errorf("recoItemId = %v, ingin %q", updated.RecoItemID, recoItemID)
	}

	reread, err := outfits.Get(ctx, base.OutfitID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(reread.Items) != 1 {
		t.Errorf("jumlah item = %d, ingin 1 setelah penggantian", len(reread.Items))
	}

	deletedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := outfits.Update(ctx, base.OutfitID, func(o *model.Outfit) error {
		o.DeletedAt = &deletedAt
		o.UpdatedAt = deletedAt
		return nil
	}); err != nil {
		t.Fatalf("Update() soft delete error = %v", err)
	}

	// Baris tetap terbaca supaya pemanggil bisa membedakan 410 dari 404.
	gone, err := outfits.Get(ctx, base.OutfitID)
	if err != nil {
		t.Fatalf("Get() setelah soft delete error = %v", err)
	}
	if !gone.Deleted() {
		t.Error("deletedAt tidak tersimpan")
	}

	rows, _, err := outfits.List(ctx, store.OutfitFilter{UserID: base.UserID}, nil, 20)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("daftar = %d baris, ingin 0 setelah soft delete", len(rows))
	}
}

func TestOutfitListBerhalamanDenganKeyset(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	older := sampleOutfit("otf_test03", "550e8400-e29b-41d4-a716-446655440002", now.Add(-time.Hour))
	newer := sampleOutfit("otf_test04", "550e8400-e29b-41d4-a716-446655440003", now)
	for _, o := range []model.Outfit{older, newer} {
		if err := outfits.Create(ctx, o); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	first, hasMore, err := outfits.List(ctx, store.OutfitFilter{UserID: newer.UserID}, nil, 1)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if !hasMore || len(first) != 1 || first[0].OutfitID != newer.OutfitID {
		t.Fatalf("halaman pertama = %+v (hasMore=%v), ingin outfit terbaru", first, hasMore)
	}

	cursor := store.OutfitCursor{Recency: &paging.KeysetCursor{At: first[0].UpdatedAt, ID: first[0].OutfitID}}
	second, hasMore, err := outfits.List(ctx, store.OutfitFilter{UserID: newer.UserID}, &cursor, 1)
	if err != nil {
		t.Fatalf("ListByUser() halaman kedua error = %v", err)
	}
	if hasMore {
		t.Error("hasMore = true padahal hanya ada dua baris")
	}
	if len(second) != 1 || second[0].OutfitID != older.OutfitID {
		t.Fatalf("halaman kedua = %+v, ingin outfit lama", second)
	}
}

func TestOutfitListCariKeywordDanOutfitID(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	y2k := sampleOutfit("otf_test06", "550e8400-e29b-41d4-a716-446655440005", now)
	pop := sampleOutfit("otf_test07", "550e8400-e29b-41d4-a716-446655440006", now.Add(-time.Minute))
	pop.Name = "Girly Pop 100% Casual"
	for _, o := range []model.Outfit{y2k, pop} {
		if err := outfits.Create(ctx, o); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	cases := []struct {
		name   string
		filter store.OutfitFilter
		want   []string
	}{
		{"keyword tanpa peduli huruf besar-kecil", store.OutfitFilter{Keyword: "sTrEeTwEaR"}, []string{y2k.OutfitID}},
		{"keyword cocok sebagian", store.OutfitFilter{Keyword: "casual"}, []string{pop.OutfitID}},
		{"persen dicari apa adanya", store.OutfitFilter{Keyword: "100%"}, []string{pop.OutfitID}},
		// "%" hanya cocok dengan baris yang benar-benar memuatnya; kalau ia
		// lolos sebagai wildcard, kedua outfit akan ikut terbawa.
		{"persen bukan wildcard", store.OutfitFilter{Keyword: "%"}, []string{pop.OutfitID}},
		{"underscore bukan wildcard", store.OutfitFilter{Keyword: "_"}, nil},
		{"satu outfitId", store.OutfitFilter{OutfitIDs: []string{pop.OutfitID}}, []string{pop.OutfitID}},
		{"beberapa outfitId", store.OutfitFilter{OutfitIDs: []string{y2k.OutfitID, pop.OutfitID}}, []string{y2k.OutfitID, pop.OutfitID}},
		{"outfitId tak dikenal", store.OutfitFilter{OutfitIDs: []string{"otf_tidakada"}}, nil},
		{"keyword dan outfitId dipadukan", store.OutfitFilter{OutfitIDs: []string{y2k.OutfitID, pop.OutfitID}, Keyword: "pop"}, []string{pop.OutfitID}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, _, err := outfits.List(ctx, tc.filter, nil, 20)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			got := make([]string, 0, len(rows))
			for _, o := range rows {
				got = append(got, o.OutfitID)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("List() = %v, ingin %v", got, tc.want)
			}
			for i, id := range tc.want {
				if got[i] != id {
					t.Fatalf("List() = %v, ingin %v", got, tc.want)
				}
			}
		})
	}
}

func TestOutfitResolveMelewatiYangTerhapus(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	hidup := sampleOutfit("otf_test05", "550e8400-e29b-41d4-a716-446655440004", time.Now().UTC())
	terhapus := sampleOutfit("otf_test06", "550e8400-e29b-41d4-a716-446655440005", time.Now().UTC())
	for _, o := range []model.Outfit{hidup, terhapus} {
		if err := outfits.Create(ctx, o); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	deletedAt := time.Now().UTC()
	if _, err := outfits.Update(ctx, terhapus.OutfitID, func(o *model.Outfit) error {
		o.DeletedAt = &deletedAt
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	found, err := outfits.ListByReferenceIDs(ctx, []string{hidup.ReferenceID, terhapus.ReferenceID})
	if err != nil {
		t.Fatalf("ListByReferenceIDs() error = %v", err)
	}
	if len(found) != 1 || found[0].OutfitID != hidup.OutfitID {
		t.Errorf("hasil = %+v, ingin hanya outfit yang hidup", found)
	}
}

func TestOutfitTidakAda(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)

	_, err := outfits.Get(context.Background(), "otf_entah")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, ingin store.ErrNotFound", err)
	}
}

func TestTransactionIdempotencyBentrok(t *testing.T) {
	pool := newPool(t)
	transactions := postgres.NewTransactions(pool)
	ctx := context.Background()

	tx := model.Transaction{
		TxID:           "tx_test01",
		UserID:         627278822,
		UniverseID:     8739264611,
		PlaceID:        124012682058672,
		JobID:          "b1f0e2c4-7a3d-4c11-9f88-2ab5c9e01d77",
		IdempotencyKey: "kunci-sama",
		Status:         "success",
		OccurredAt:     time.Now().UTC().Truncate(time.Microsecond),
		ReceivedAt:     time.Now().UTC().Truncate(time.Microsecond),
		Items: []model.TransactionItem{
			{AssetID: 78872304386489, Price: 69, Result: model.ResultSuccess},
			{AssetID: 14433369343, Price: 79, Result: model.ResultAborted},
		},
	}
	if err := transactions.Create(ctx, tx); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Retry yang tiba bersamaan memakai txId berbeda tapi kunci sama.
	racer := tx
	racer.TxID = "tx_test02"
	err := transactions.Create(ctx, racer)
	if !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("error = %v, ingin store.ErrIdempotencyConflict", err)
	}

	found, ok := transactions.ByIdempotencyKey(ctx, tx.IdempotencyKey)
	if !ok {
		t.Fatal("transaksi pemenang tidak ditemukan lewat kunci idempotensi")
	}
	if found.TxID != tx.TxID {
		t.Errorf("txId = %q, ingin %q", found.TxID, tx.TxID)
	}
	if found.RobuxTotal() != 69 {
		t.Errorf("robuxTotal = %d, ingin 69", found.RobuxTotal())
	}
	if len(found.Items) != 2 {
		t.Errorf("jumlah item = %d, ingin 2", len(found.Items))
	}
}

func TestTemplateBelumTerdaftar(t *testing.T) {
	pool := newPool(t)
	templates := postgres.NewTemplates(pool)
	ctx := context.Background()

	if _, err := templates.Get(ctx, "88484288792766"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, err := templates.Get(ctx, "77771111222233"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, ingin store.ErrNotFound", err)
	}
}

func TestTemplateEnsureMendaftarkanSekaliSaja(t *testing.T) {
	pool := newPool(t)
	templates := postgres.NewTemplates(pool)
	ctx := context.Background()

	rig := model.BodyTemplate{
		TemplateID:    "77771111222233",
		Name:          "Rig Baru",
		Gender:        "F",
		SourceAssetID: 77771111222233,
		CreatedAt:     time.Now().UTC().Truncate(time.Microsecond),
	}

	saved, created, err := templates.Ensure(ctx, rig)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !created {
		t.Error("created = false pada pendaftaran pertama")
	}
	if saved.Name != rig.Name || saved.SourceAssetID != rig.SourceAssetID {
		t.Errorf("hasil ensure = %+v", saved)
	}

	// Pemakaian berikutnya tidak boleh menimpa nama yang sudah diisi.
	polos := rig
	polos.Name = ""
	polos.Gender = "?"
	again, created, err := templates.Ensure(ctx, polos)
	if err != nil {
		t.Fatalf("Ensure() kedua error = %v", err)
	}
	if created {
		t.Error("created = true padahal rig sudah terdaftar")
	}
	if again.Name != "Rig Baru" || again.Gender != "F" {
		t.Errorf("rig terdaftar tertimpa: %+v", again)
	}
}

func TestTemplateListDanUpdate(t *testing.T) {
	pool := newPool(t)
	templates := postgres.NewTemplates(pool)
	ctx := context.Background()

	if _, _, err := templates.Ensure(ctx, model.BodyTemplate{
		TemplateID:    "77771111222233",
		SourceAssetID: 77771111222233,
		Gender:        "?",
		CreatedAt:     time.Now().UTC().Truncate(time.Microsecond),
	}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	rows, hasMore, err := templates.List(ctx, nil, 20)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if hasMore || len(rows) != 2 {
		t.Fatalf("List() = %d baris (hasMore=%v), ingin 2", len(rows), hasMore)
	}
	if rows[0].TemplateID != "77771111222233" {
		t.Errorf("urutan salah: %q di depan, ingin rig terbaru", rows[0].TemplateID)
	}

	updated, err := templates.Update(ctx, "77771111222233", func(tpl *model.BodyTemplate) error {
		tpl.Name = "Rig Cewek V2"
		tpl.Gender = "F"
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "Rig Cewek V2" || updated.Gender != "F" {
		t.Errorf("hasil update = %+v", updated)
	}

	if _, err := templates.Update(ctx, "99999999999999", func(*model.BodyTemplate) error { return nil }); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Update() rig tak dikenal error = %v, ingin store.ErrNotFound", err)
	}
}

// TestOutfitSearchTrigramDanEmbedding menguji jalur pencarian hybrid: salah
// ketik ditangkap trigram, dan embedding — bila ada — ikut mengangkat
// peringkat lewat RRF.
func TestOutfitSearchTrigramDanEmbedding(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	zappeto := sampleOutfit("otf_test20", "550e8400-e29b-41d4-a716-446655440020", now)
	zappeto.Name = "Aiche ZAPPETO"
	other := sampleOutfit("otf_test21", "550e8400-e29b-41d4-a716-446655440021", now.Add(-time.Minute))
	other.Name = "Girly Pop Casual"
	for _, o := range []model.Outfit{zappeto, other} {
		if err := outfits.Create(ctx, o); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	// Salah ketik tetap menemukan lewat trigram, tanpa embedding sama sekali.
	for _, q := range []string{"zappeto", "zepeto", "zeppeto"} {
		rows, err := outfits.Search(ctx, store.OutfitFilter{Keyword: q}, nil, 10)
		if err != nil {
			t.Fatalf("Search(%q) error = %v", q, err)
		}
		if len(rows) == 0 || rows[0].OutfitID != zappeto.OutfitID {
			t.Errorf("Search(%q) = %d baris, ingin %s di urutan pertama", q, len(rows), zappeto.OutfitID)
		}
	}

	// Kata yang sama sekali tidak mirip tidak boleh ikut.
	rows, err := outfits.Search(ctx, store.OutfitFilter{Keyword: "naga merah"}, nil, 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Search(naga merah) = %d baris, ingin 0", len(rows))
	}

	// Cabang semantik: setelah embedding tersimpan, query dengan vector yang
	// searah menemukan baris itu walau kata kuncinya tidak mirip sama sekali.
	embedding := make([]float32, 1536)
	embedding[0] = 1
	if err := outfits.SetNameEmbedding(ctx, zappeto.OutfitID, embedding); err != nil {
		t.Fatalf("SetNameEmbedding() error = %v", err)
	}
	rows, err = outfits.Search(ctx, store.OutfitFilter{Keyword: "kata tak mirip"}, embedding, 10)
	if err != nil {
		t.Fatalf("Search() dengan embedding error = %v", err)
	}
	if len(rows) != 1 || rows[0].OutfitID != zappeto.OutfitID {
		t.Fatalf("Search() semantik = %+v, ingin hanya %s", rows, zappeto.OutfitID)
	}

	// SetNameEmbedding ke outfit yang tidak ada harus ErrNotFound.
	if err := outfits.SetNameEmbedding(ctx, "otf_tidakada", embedding); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetNameEmbedding(tak dikenal) error = %v, ingin ErrNotFound", err)
	}
}

// TestSpendStatsMenghitungBundleSekali menguji SQL spend yang men-dedup harga
// bundle: bagian paket membawa harga bundle induk berulang, dan LifetimeSpend
// hanya boleh menghitungnya sekali per (transaksi, bundle).
func TestSpendStatsMenghitungBundleSekali(t *testing.T) {
	pool := newPool(t)
	transactions := postgres.NewTransactions(pool)
	cashback := postgres.NewCashback(pool)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	tx := model.Transaction{
		TxID:           "tx_bundle01",
		UserID:         627278822,
		IdempotencyKey: "kunci-bundle-01",
		Status:         "success",
		OccurredAt:     now,
		ReceivedAt:     now,
		Items: []model.TransactionItem{
			{AssetID: 121390054271388, Price: 55, Result: model.ResultSuccess},
			{AssetID: 429786881, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
			{AssetID: 429786958, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
			{AssetID: 429787041, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
			{AssetID: 619528412, Price: 250, Result: model.ResultSuccess, BundleID: 928374650},
			{AssetID: 619528641, Price: 250, Result: model.ResultSuccess, BundleID: 928374650},
			{AssetID: 999, Price: 400, Result: model.ResultFailed, BundleID: 555},
		},
	}
	if err := transactions.Create(ctx, tx); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	stats, err := cashback.SpendStats(ctx, tx.UserID, "", now.AddDate(0, 0, -3))
	if err != nil {
		t.Fatalf("SpendStats() error = %v", err)
	}
	if want := 55 + 445 + 250; stats.LifetimeSpend != want {
		t.Errorf("LifetimeSpend = %d, ingin %d (bundle dihitung sekali)", stats.LifetimeSpend, want)
	}
	if stats.PurchaseCount != 1 {
		t.Errorf("PurchaseCount = %d, ingin 1", stats.PurchaseCount)
	}

	// Item transaksi terbaca kembali lengkap dengan bundleId-nya.
	rows, _, err := transactions.ListByUser(ctx, tx.UserID, nil, 10)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListByUser() = %d transaksi, ingin 1", len(rows))
	}
	if got := rows[0].RobuxTotal(); got != 55+445+250 {
		t.Errorf("RobuxTotal() dari baris tersimpan = %d, ingin %d", got, 55+445+250)
	}
}

// Keunikan like ditegakkan kunci primer OUTFIT_LIKE, bukan pemeriksaan di
// aplikasi — test ini yang membuktikannya sampai ke database.
func TestOutfitLikeIdempotenDanMenaikkanCounter(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	outfit := sampleOutfit("otf_like01", "550e8400-e29b-41d4-a716-446655440000", time.Now().UTC().Truncate(time.Microsecond))
	if err := outfits.Create(ctx, outfit); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now := time.Now().UTC()
	counts, err := outfits.Like(ctx, outfit.OutfitID, 111, now)
	if err != nil {
		t.Fatalf("Like() error = %v", err)
	}
	if !counts.Changed || counts.LikeCount != 1 {
		t.Fatalf("like pertama = %+v, ingin changed=true likeCount=1", counts)
	}

	// Like berulang tetap melaporkan angka yang benar, bukan nol.
	if counts, err = outfits.Like(ctx, outfit.OutfitID, 111, now); err != nil {
		t.Fatalf("Like() kedua error = %v", err)
	}
	if counts.Changed || counts.LikeCount != 1 {
		t.Errorf("like berulang = %+v, ingin changed=false likeCount=1", counts)
	}

	got, err := outfits.Get(ctx, outfit.OutfitID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.LikeCount != 1 {
		t.Errorf("likeCount = %d, ingin 1", got.LikeCount)
	}

	// Unlike mengembalikan counter ke nol, dan unlike kedua tidak membuatnya
	// negatif.
	if _, err := outfits.Unlike(ctx, outfit.OutfitID, 111); err != nil {
		t.Fatalf("Unlike() error = %v", err)
	}
	if counts, err = outfits.Unlike(ctx, outfit.OutfitID, 111); err != nil {
		t.Fatalf("Unlike() kedua error = %v", err)
	}
	if counts.Changed || counts.LikeCount != 0 {
		t.Errorf("unlike berulang = %+v, ingin changed=false likeCount=0", counts)
	}
	if got, err = outfits.Get(ctx, outfit.OutfitID); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.LikeCount != 0 {
		t.Errorf("likeCount setelah unlike = %d, ingin 0", got.LikeCount)
	}
}

// View bersifat append-only: pemain yang sama membuka outfit tiga kali memang
// tiga baris, bukan satu.
func TestOutfitViewSelaluMenambahBaris(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	outfit := sampleOutfit("otf_view01", "550e8400-e29b-41d4-a716-446655440000", time.Now().UTC().Truncate(time.Microsecond))
	if err := outfits.Create(ctx, outfit); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	for i := 0; i < 3; i++ {
		counts, err := outfits.RecordView(ctx, outfit.OutfitID, 111, time.Now().UTC())
		if err != nil {
			t.Fatalf("RecordView() ke-%d error = %v", i+1, err)
		}
		// Angka balasan datang dari UPDATE ... RETURNING, jadi harus naik tiap
		// panggilan tanpa pembacaan terpisah.
		if counts.ViewCount != i+1 {
			t.Errorf("viewCount pada panggilan ke-%d = %d, ingin %d", i+1, counts.ViewCount, i+1)
		}
	}
	// Penonton anonim tetap tercatat, dengan user_id NULL.
	if _, err := outfits.RecordView(ctx, outfit.OutfitID, 0, time.Now().UTC()); err != nil {
		t.Fatalf("RecordView() anonim error = %v", err)
	}

	got, err := outfits.Get(ctx, outfit.OutfitID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ViewCount != 4 {
		t.Errorf("viewCount = %d, ingin 4", got.ViewCount)
	}

	var rows, anon int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE user_id IS NULL) FROM outfit_view WHERE outfit_id = $1`,
		outfit.OutfitID).Scan(&rows, &anon); err != nil {
		t.Fatalf("hitung outfit_view error = %v", err)
	}
	if rows != 4 || anon != 1 {
		t.Errorf("outfit_view = %d baris (%d anonim), ingin 4 dan 1", rows, anon)
	}
}

func TestOutfitListMostLikedBerhalamanDenganKeyset(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Microsecond)
	populer := sampleOutfit("otf_pop001", "550e8400-e29b-41d4-a716-446655440000", base)
	sepi := sampleOutfit("otf_pop002", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", base.Add(time.Minute))
	for _, o := range []model.Outfit{populer, sepi} {
		if err := outfits.Create(ctx, o); err != nil {
			t.Fatalf("Create(%s) error = %v", o.OutfitID, err)
		}
	}

	// sepi lebih baru, jadi urutan terbaru menaruhnya di atas. Dua like membuat
	// populer memimpin urutan mostLiked — kalau sort tidak benar-benar dipakai,
	// test ini gagal alih-alih lolos karena kebetulan.
	for _, userID := range []int64{111, 222} {
		if _, err := outfits.Like(ctx, populer.OutfitID, userID, time.Now().UTC()); err != nil {
			t.Fatalf("Like() error = %v", err)
		}
	}

	filter := store.OutfitFilter{Sort: store.SortMostLiked}
	first, hasMore, err := outfits.List(ctx, filter, nil, 1)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !hasMore || len(first) != 1 || first[0].OutfitID != populer.OutfitID {
		t.Fatalf("halaman pertama = %+v (hasMore=%v), ingin outfit terpopuler", first, hasMore)
	}
	if first[0].LikeCount != 2 {
		t.Errorf("likeCount = %d, ingin 2", first[0].LikeCount)
	}

	cursor := store.OutfitCursor{Count: &paging.CountCursor{Count: first[0].LikeCount, ID: first[0].OutfitID}}
	second, hasMore, err := outfits.List(ctx, filter, &cursor, 1)
	if err != nil {
		t.Fatalf("List() halaman kedua error = %v", err)
	}
	if hasMore {
		t.Error("hasMore = true padahal hanya ada dua baris")
	}
	if len(second) != 1 || second[0].OutfitID != sepi.OutfitID {
		t.Fatalf("halaman kedua = %+v, ingin %s", second, sepi.OutfitID)
	}
}

func TestOutfitLikedMenandaiHanyaMilikPemain(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Microsecond)
	disukai := sampleOutfit("otf_lkd001", "550e8400-e29b-41d4-a716-446655440000", base)
	tidak := sampleOutfit("otf_lkd002", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", base)
	for _, o := range []model.Outfit{disukai, tidak} {
		if err := outfits.Create(ctx, o); err != nil {
			t.Fatalf("Create(%s) error = %v", o.OutfitID, err)
		}
	}
	if _, err := outfits.Like(ctx, disukai.OutfitID, 111, time.Now().UTC()); err != nil {
		t.Fatalf("Like() error = %v", err)
	}

	liked, err := outfits.Liked(ctx, 111, []string{disukai.OutfitID, tidak.OutfitID})
	if err != nil {
		t.Fatalf("Liked() error = %v", err)
	}
	if !liked[disukai.OutfitID] || liked[tidak.OutfitID] {
		t.Errorf("liked = %v, ingin hanya %s", liked, disukai.OutfitID)
	}

	// Pemain lain tidak ikut kebagian penanda milik orang.
	other, err := outfits.Liked(ctx, 222, []string{disukai.OutfitID})
	if err != nil {
		t.Fatalf("Liked() pemain lain error = %v", err)
	}
	if len(other) != 0 {
		t.Errorf("liked pemain lain = %v, ingin kosong", other)
	}
}

// Like pada outfit yang sudah di-soft-delete ditolak: barisnya masih ada demi
// referenceId yang beredar, tapi tidak boleh menerima interaksi baru.
func TestOutfitLikeMenolakYangTerhapus(t *testing.T) {
	pool := newPool(t)
	outfits := postgres.NewOutfits(pool)
	ctx := context.Background()

	outfit := sampleOutfit("otf_del001", "550e8400-e29b-41d4-a716-446655440000", time.Now().UTC().Truncate(time.Microsecond))
	if err := outfits.Create(ctx, outfit); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	deletedAt := time.Now().UTC()
	if _, err := outfits.Update(ctx, outfit.OutfitID, func(o *model.Outfit) error {
		o.DeletedAt = &deletedAt
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, err := outfits.Like(ctx, outfit.OutfitID, 111, time.Now().UTC()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Like() pada outfit terhapus error = %v, ingin ErrNotFound", err)
	}
	if _, err := outfits.RecordView(ctx, outfit.OutfitID, 111, time.Now().UTC()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("RecordView() pada outfit terhapus error = %v, ingin ErrNotFound", err)
	}
}

// --- kunci API -------------------------------------------------------------

func newAPIKey(t *testing.T, name string, expiresAt *time.Time) (auth.Key, string) {
	t.Helper()

	token, err := auth.Generate(auth.EnvTest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	return auth.Key{
		KeyID:     token.KeyID,
		Hash:      token.Hash,
		Name:      name,
		Scopes:    auth.Roles["game-server"],
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		ExpiresAt: expiresAt,
	}, token.Secret
}

func TestAPIKeyTulisLaluBaca(t *testing.T) {
	pool := newPool(t)
	keys := postgres.NewAPIKeys(pool)
	ctx := context.Background()

	nanti := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Microsecond)
	want, secret := newAPIKey(t, "roblox-game-server", &nanti)
	if err := keys.Create(ctx, want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := keys.ByKeyID(ctx, want.KeyID)
	if err != nil {
		t.Fatalf("ByKeyID() error = %v", err)
	}
	if got.Name != want.Name {
		t.Errorf("name = %q, ingin %q", got.Name, want.Name)
	}
	if !auth.Equal(got.Hash, auth.HashToken(secret)) {
		t.Error("hash tersimpan tidak cocok dengan token aslinya")
	}
	if len(got.Scopes) != len(want.Scopes) {
		t.Errorf("scopes = %v, ingin %v", got.Scopes, want.Scopes)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(nanti) {
		t.Errorf("expiresAt = %v, ingin %v", got.ExpiresAt, nanti)
	}
	if !got.Usable(time.Now().UTC()) {
		t.Error("kunci baru tidak bisa dipakai")
	}

	// Yang tersimpan hanya hash: token utuhnya tidak boleh ada di baris mana pun.
	var found int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM api_key WHERE encode(token_hash, 'escape') LIKE '%' || $1 || '%'`,
		secret).Scan(&found); err != nil {
		t.Fatalf("hitung error = %v", err)
	}
	if found != 0 {
		t.Error("token asli ikut tersimpan di database")
	}
}

func TestAPIKeyTidakAda(t *testing.T) {
	pool := newPool(t)
	keys := postgres.NewAPIKeys(pool)

	if _, err := keys.ByKeyID(context.Background(), "tidakada"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ByKeyID() error = %v, ingin ErrNotFound", err)
	}
}

// Kolom last_used_at pernah diam-diam tidak pernah terisi karena parameternya
// tidak di-cast, dan kegagalannya cuma di-log. Test ini yang menahannya.
func TestAPIKeyTouchLastUsedBenarBenarMenulis(t *testing.T) {
	pool := newPool(t)
	keys := postgres.NewAPIKeys(pool)
	ctx := context.Background()

	key, _ := newAPIKey(t, "dipakai", nil)
	if err := keys.Create(ctx, key); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now := time.Now().UTC()
	if err := keys.TouchLastUsed(ctx, key.KeyID, now); err != nil {
		t.Fatalf("TouchLastUsed() error = %v", err)
	}

	got, err := keys.ByKeyID(ctx, key.KeyID)
	if err != nil {
		t.Fatalf("ByKeyID() error = %v", err)
	}
	if got.LastUsedAt == nil {
		t.Fatal("lastUsedAt masih kosong setelah TouchLastUsed()")
	}

	// Pemakaian berikutnya dalam satu menit tidak menulis ulang: tanpa ambang
	// ini, tiap request jadi satu UPDATE ke baris yang sama.
	first := *got.LastUsedAt
	if err := keys.TouchLastUsed(ctx, key.KeyID, now.Add(30*time.Second)); err != nil {
		t.Fatalf("TouchLastUsed() kedua error = %v", err)
	}
	if got, err = keys.ByKeyID(ctx, key.KeyID); err != nil {
		t.Fatalf("ByKeyID() error = %v", err)
	}
	if !got.LastUsedAt.Equal(first) {
		t.Errorf("lastUsedAt ditulis ulang dalam satu menit: %v -> %v", first, *got.LastUsedAt)
	}

	// Lewat satu menit, barulah diperbarui.
	if err := keys.TouchLastUsed(ctx, key.KeyID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("TouchLastUsed() ketiga error = %v", err)
	}
	if got, err = keys.ByKeyID(ctx, key.KeyID); err != nil {
		t.Fatalf("ByKeyID() error = %v", err)
	}
	if got.LastUsedAt.Equal(first) {
		t.Error("lastUsedAt tidak diperbarui setelah lewat satu menit")
	}
}

func TestAPIKeyRevokeIdempotenDanMempertahankanWaktuPertama(t *testing.T) {
	pool := newPool(t)
	keys := postgres.NewAPIKeys(pool)
	ctx := context.Background()

	key, _ := newAPIKey(t, "dicabut", nil)
	if err := keys.Create(ctx, key); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	first := time.Now().UTC().Truncate(time.Microsecond)
	if err := keys.Revoke(ctx, key.KeyID, first); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if err := keys.Revoke(ctx, key.KeyID, first.Add(time.Hour)); err != nil {
		t.Fatalf("Revoke() kedua error = %v", err)
	}

	got, err := keys.ByKeyID(ctx, key.KeyID)
	if err != nil {
		t.Fatalf("ByKeyID() error = %v", err)
	}
	if got.RevokedAt == nil || !got.RevokedAt.Equal(first) {
		t.Errorf("revokedAt = %v, ingin tetap %v (pencabutan pertama)", got.RevokedAt, first)
	}
	if got.Usable(time.Now().UTC()) {
		t.Error("kunci yang dicabut masih bisa dipakai")
	}

	if err := keys.Revoke(ctx, "tidakada", first); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Revoke() kunci tidak ada error = %v, ingin ErrNotFound", err)
	}
}

func TestAPIKeyListTerbaruDulu(t *testing.T) {
	pool := newPool(t)
	keys := postgres.NewAPIKeys(pool)
	ctx := context.Background()

	lama, _ := newAPIKey(t, "lama", nil)
	lama.CreatedAt = time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	baru, _ := newAPIKey(t, "baru", nil)
	for _, key := range []auth.Key{lama, baru} {
		if err := keys.Create(ctx, key); err != nil {
			t.Fatalf("Create(%s) error = %v", key.Name, err)
		}
	}

	rows, err := keys.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 2 || rows[0].Name != "baru" {
		t.Fatalf("List() = %v, ingin terbaru dulu", rows)
	}
}
