package service_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Asset yang dipakai bersama oleh test, semuanya ada di data contoh.
const (
	assetHair    = int64(78872304386489)
	assetJacket  = int64(14433369343)
	assetFace    = int64(116123466288288)
	seededOutfit = "otf_9f2a41"
	seededUser   = int64(627278822)
	seededRefID  = "550e8400-e29b-41d4-a716-446655440000"
	unknownRefID = "00000000-0000-0000-0000-000000000000"

	// devRig sudah terdaftar di data contoh; newRig belum pernah dilihat backend.
	devRig = "88484288792766"
	newRig = "77771111222233"
)

func newOutfitService(t *testing.T) *service.Outfits {
	t.Helper()

	outfits := store.NewMemoryOutfits()
	templates := store.NewMemoryTemplates()
	store.SeedData(templates, outfits)

	return service.NewOutfits(outfits, templates)
}

func validCreateInput() service.CreateOutfitInput {
	return service.CreateOutfitInput{
		UserID:     seededUser,
		TemplateID: devRig,
		Name:       "Y2K Streetwear",
		IsPublic:   false,
		CustomTags: []string{"category:y2k", "gender:male"},
		Items: []model.OutfitItem{
			{AssetID: assetHair, Slot: "Hair"},
			{AssetID: assetJacket, Slot: "Jacket"},
		},
	}
}

// requireAPIError menegaskan err adalah *apierr.Error dengan status dan kode
// yang diharapkan.
func requireAPIError(t *testing.T, err error, wantStatus int, wantCode string) *apierr.Error {
	t.Helper()

	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, ingin *apierr.Error", err)
	}
	if apiErr.Status != wantStatus || apiErr.Code != wantCode {
		t.Fatalf("error = %d/%s, ingin %d/%s", apiErr.Status, apiErr.Code, wantStatus, wantCode)
	}
	return apiErr
}

func TestCreateOutfitMembangkitkanReferenceID(t *testing.T) {
	svc := newOutfitService(t)

	outfit, err := svc.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if outfit.OutfitID == "" || outfit.ReferenceID == "" {
		t.Fatalf("id kosong: outfitId=%q referenceId=%q", outfit.OutfitID, outfit.ReferenceID)
	}
	if len(outfit.ReferenceID) != 36 {
		t.Errorf("referenceId = %q, ingin UUID 36 karakter", outfit.ReferenceID)
	}
	if outfit.RecoItemID != nil {
		t.Errorf("recoItemId = %v, ingin nil sebelum RegisterItemAsync", *outfit.RecoItemID)
	}
	if outfit.CreatedAt.IsZero() || !outfit.CreatedAt.Equal(outfit.UpdatedAt) {
		t.Errorf("timestamp tidak wajar: createdAt=%v updatedAt=%v", outfit.CreatedAt, outfit.UpdatedAt)
	}
}

func TestCreateOutfitValidasi(t *testing.T) {
	tests := map[string]struct {
		mutate     func(*service.CreateOutfitInput)
		wantStatus int
		wantCode   string
	}{
		"tag ada koma": {
			func(in *service.CreateOutfitInput) { in.CustomTags = []string{"category:y2k,street"} },
			http.StatusUnprocessableEntity, "invalid_custom_tag",
		},
		"slot bentrok": {
			func(in *service.CreateOutfitInput) {
				in.Items = append(in.Items, model.OutfitItem{AssetID: assetFace, Slot: "Hair"})
			},
			http.StatusConflict, "duplicate_slot",
		},
		"assetId kosong": {
			func(in *service.CreateOutfitInput) {
				in.Items = append(in.Items, model.OutfitItem{Slot: "Shoulder"})
			},
			http.StatusUnprocessableEntity, "invalid_asset_id",
		},
		"name item kepanjangan": {
			func(in *service.CreateOutfitInput) {
				in.Items = append(in.Items, model.OutfitItem{
					AssetID: 120044550099, Slot: "Hat",
					Name: strings.Repeat("a", service.MaxItemNameLen+1),
				})
			},
			http.StatusUnprocessableEntity, "invalid_item_name",
		},
		"assetType kepanjangan": {
			func(in *service.CreateOutfitInput) {
				in.Items = append(in.Items, model.OutfitItem{
					AssetID: 120044550098, Slot: "Hat",
					AssetType: strings.Repeat("a", service.MaxAssetTypeLen+1),
				})
			},
			http.StatusUnprocessableEntity, "invalid_asset_type",
		},
		"price negatif": {
			func(in *service.CreateOutfitInput) {
				in.Items = append(in.Items, model.OutfitItem{AssetID: 120044550097, Slot: "Hat", Price: -1})
			},
			http.StatusUnprocessableEntity, "invalid_item_price",
		},
		"templateId bukan asset id": {
			func(in *service.CreateOutfitInput) { in.TemplateID = "male_2" },
			http.StatusUnprocessableEntity, "invalid_template_id",
		},
		"templateId kosong": {
			func(in *service.CreateOutfitInput) { in.TemplateID = "  " },
			http.StatusUnprocessableEntity, "missing_template",
		},
		"tanpa nama": {
			func(in *service.CreateOutfitInput) { in.Name = "   " },
			http.StatusUnprocessableEntity, "missing_name",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc := newOutfitService(t)
			in := validCreateInput()
			tc.mutate(&in)

			_, err := svc.Create(context.Background(), in)
			requireAPIError(t, err, tc.wantStatus, tc.wantCode)
		})
	}
}

func TestCreateOutfitMendaftarkanRigBaru(t *testing.T) {
	outfits := store.NewMemoryOutfits()
	templates := store.NewMemoryTemplates()
	store.SeedData(templates, outfits)
	svc := service.NewOutfits(outfits, templates)
	ctx := context.Background()

	// Rig ini baru saja di-upload ke Roblox; backend belum pernah melihatnya
	// sebelum outfit ini dibuat.
	if _, err := templates.Get(ctx, newRig); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("prasyarat gagal: rig %s seharusnya belum terdaftar (err=%v)", newRig, err)
	}

	in := validCreateInput()
	in.TemplateID = newRig
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create() error = %v, ingin rig baru diterima", err)
	}
	if created.TemplateID != newRig {
		t.Errorf("templateId = %q, ingin %q", created.TemplateID, newRig)
	}

	registered, err := templates.Get(ctx, newRig)
	if err != nil {
		t.Fatalf("rig tidak ikut terdaftar: %v", err)
	}
	if registered.SourceAssetID != 77771111222233 {
		t.Errorf("sourceAssetId = %d, ingin 77771111222233", registered.SourceAssetID)
	}
	if registered.Gender != "?" || registered.Name != "" {
		t.Errorf("rig otomatis = %+v, ingin nama kosong dan gender '?'", registered)
	}
}

func TestCreateOutfitTidakMenimpaRigTerdaftar(t *testing.T) {
	outfits := store.NewMemoryOutfits()
	templates := store.NewMemoryTemplates()
	store.SeedData(templates, outfits)
	svc := service.NewOutfits(outfits, templates)
	ctx := context.Background()

	before, err := templates.Get(ctx, devRig)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if _, err := svc.Create(ctx, validCreateInput()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	after, err := templates.Get(ctx, devRig)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if after.Name != before.Name || after.Gender != before.Gender {
		t.Errorf("rig terdaftar tertimpa: %+v -> %+v", before, after)
	}
}

func TestCreateOutfitTagBerkomaMelaporkanFieldnya(t *testing.T) {
	svc := newOutfitService(t)
	in := validCreateInput()
	in.CustomTags = []string{"aman", "category:y2k,street"}

	_, err := svc.Create(context.Background(), in)

	apiErr := requireAPIError(t, err, http.StatusUnprocessableEntity, "invalid_custom_tag")
	if len(apiErr.Details) != 1 {
		t.Fatalf("details = %v, ingin tepat satu entri", apiErr.Details)
	}
	detail, ok := apiErr.Details[0].(map[string]any)
	if !ok || detail["field"] != "customTags[1]" {
		t.Errorf("detail = %v, ingin field customTags[1]", apiErr.Details[0])
	}
}

func TestGetOutfitMembawaDetailItem(t *testing.T) {
	svc := newOutfitService(t)

	outfit, err := svc.Get(context.Background(), seededOutfit)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	var found bool
	for _, item := range outfit.Items {
		if item.AssetID != assetHair {
			continue
		}
		found = true
		if item.Slot != "Hair" || item.Name != "BLOND BARREL TWISTS DREADS" ||
			item.AssetType != "HairAccessory" || item.Price != 69 {
			t.Errorf("item = %+v, ingin detail lengkap ikut terbawa", item)
		}
	}
	if !found {
		t.Fatalf("asset %d tidak ada di outfit contoh", assetHair)
	}
}

func TestSoftDeleteLaluGetJadi410(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	deleted, err := svc.SoftDelete(ctx, service.Actor{}, seededOutfit)
	if err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}
	if deleted.DeletedAt == nil {
		t.Fatal("deletedAt masih nil setelah soft delete")
	}
	if deleted.RecoItemID == nil || *deleted.RecoItemID != "reco_7b31c9" {
		t.Errorf("recoItemId = %v, ingin ikut dikembalikan untuk RemoveItemAsync", deleted.RecoItemID)
	}

	_, err = svc.Get(ctx, seededOutfit)
	requireAPIError(t, err, http.StatusGone, "outfit_deleted")

	// Outfit yang sudah dihapus tidak boleh muncul lagi di daftar pemain.
	page, err := svc.List(ctx, service.ListOutfitFilter{UserID: seededUser}, "", 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, o := range page.Outfits {
		if o.OutfitID == seededOutfit {
			t.Error("outfit terhapus masih muncul di daftar")
		}
	}
}

func TestGetOutfitTidakAda(t *testing.T) {
	svc := newOutfitService(t)

	_, err := svc.Get(context.Background(), "otf_xxxx")
	requireAPIError(t, err, http.StatusNotFound, "outfit_not_found")
}

func TestUpdateMenyimpanRecoItemID(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, validCreateInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	recoItemID := "reco_baru01"
	isPublic := true
	updated, err := svc.Update(ctx, service.Actor{}, created.OutfitID, service.UpdateOutfitInput{
		RecoItemID: &recoItemID,
		IsPublic:   &isPublic,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.RecoItemID == nil || *updated.RecoItemID != recoItemID {
		t.Errorf("recoItemId = %v, ingin %q", updated.RecoItemID, recoItemID)
	}
	if !updated.IsPublic {
		t.Error("isPublic tidak ikut berubah")
	}
	if updated.Name != created.Name {
		t.Errorf("name = %q, ingin tetap %q", updated.Name, created.Name)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Error("updatedAt mundur setelah update")
	}
}

func TestUpdateBukanPemilikDitolak(t *testing.T) {
	svc := newOutfitService(t)
	name := "diambil alih"

	_, err := svc.Update(context.Background(), service.Actor{UserID: 111, Present: true}, seededOutfit,
		service.UpdateOutfitInput{Name: &name})

	requireAPIError(t, err, http.StatusForbidden, "not_owner")
}

func TestReplaceItemsMenggantiSeluruhKoleksi(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	updated, err := svc.ReplaceItems(ctx, service.Actor{}, seededOutfit, []model.OutfitItem{
		{AssetID: 111202422466045, Slot: "TShirt", Name: "Y2K Cargo Tee", AssetType: "Shirt", Price: 25},
	})
	if err != nil {
		t.Fatalf("ReplaceItems() error = %v", err)
	}

	if len(updated.Items) != 1 {
		t.Fatalf("jumlah item = %d, ingin 1 — koleksi lama seharusnya terganti utuh", len(updated.Items))
	}
	if item := updated.Items[0]; item.Name != "Y2K Cargo Tee" || item.AssetType != "Shirt" || item.Price != 25 {
		t.Errorf("item = %+v, ingin detail ikut tersimpan", item)
	}
}

func TestResolveMelaporkanYangTidakKetemu(t *testing.T) {
	svc := newOutfitService(t)

	found, notFound, err := svc.Resolve(context.Background(), []string{seededRefID, unknownRefID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(found) != 1 || found[0].ReferenceID != seededRefID {
		t.Errorf("found = %+v, ingin satu outfit dengan referenceId contoh", found)
	}
	if len(notFound) != 1 || notFound[0] != unknownRefID {
		t.Errorf("notFound = %v, ingin [%s]", notFound, unknownRefID)
	}
}

func TestResolveMenolakTerlaluBanyakID(t *testing.T) {
	svc := newOutfitService(t)

	ids := make([]string, service.MaxResolveIDs+1)
	for i := range ids {
		ids[i] = unknownRefID
	}

	_, _, err := svc.Resolve(context.Background(), ids)
	requireAPIError(t, err, http.StatusRequestEntityTooLarge, "too_many_ids")
}

func TestListBerhalamanDenganCursor(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	first, err := svc.List(ctx, service.ListOutfitFilter{UserID: seededUser}, "", 1)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(first.Outfits) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("halaman pertama = %+v, ingin 1 item dan ada cursor lanjutan", first)
	}

	second, err := svc.List(ctx, service.ListOutfitFilter{UserID: seededUser}, first.NextCursor, 1)
	if err != nil {
		t.Fatalf("List() halaman kedua error = %v", err)
	}
	if len(second.Outfits) != 1 {
		t.Fatalf("halaman kedua = %+v, ingin 1 item", second.Outfits)
	}
	if second.Outfits[0].OutfitID == first.Outfits[0].OutfitID {
		t.Error("halaman kedua mengulang baris yang sama")
	}
	if second.HasMore {
		t.Error("hasMore = true padahal data contoh hanya dua outfit")
	}
}

func TestListTanpaUserIDMencakupSemuaPemain(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	in := validCreateInput()
	in.UserID = 999111
	if _, err := svc.Create(ctx, in); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	all, err := svc.List(ctx, service.ListOutfitFilter{}, "", 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all.Outfits) != 3 {
		t.Fatalf("jumlah outfit = %d, ingin 3 dari semua pemain", len(all.Outfits))
	}

	mine, err := svc.List(ctx, service.ListOutfitFilter{UserID: seededUser}, "", 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(mine.Outfits) != 2 {
		t.Errorf("jumlah outfit = %d, ingin 2 milik pemain contoh", len(mine.Outfits))
	}
}

func TestListFilterIsPublic(t *testing.T) {
	svc := newOutfitService(t)
	publik := true

	page, err := svc.List(context.Background(), service.ListOutfitFilter{IsPublic: &publik}, "", 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Outfits) != 1 {
		t.Fatalf("jumlah outfit = %d, ingin 1 yang publik", len(page.Outfits))
	}
	if !page.Outfits[0].IsPublic {
		t.Errorf("outfit = %+v, ingin isPublic true", page.Outfits[0])
	}
}

func TestListMenolakCursorRusak(t *testing.T) {
	svc := newOutfitService(t)

	_, err := svc.List(context.Background(), service.ListOutfitFilter{UserID: seededUser}, "bukan-base64!!", 0)
	requireAPIError(t, err, http.StatusBadRequest, "invalid_cursor")
}
