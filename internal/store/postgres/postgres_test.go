package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
		TRUNCATE transaction_item, transaction, outfit_item, outfit, body_template, player
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

	cursor := paging.KeysetCursor{At: first[0].UpdatedAt, ID: first[0].OutfitID}
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
