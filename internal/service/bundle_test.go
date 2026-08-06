package service_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/model"
)

// TestCreateOutfitMembawaFieldBundle memastikan bundleId/bundleName item dan
// thumbnailAssetId outfit tersimpan dan terbaca kembali apa adanya.
func TestCreateOutfitMembawaFieldBundle(t *testing.T) {
	svc := newOutfitService(t)

	in := validCreateInput()
	in.ThumbnailAssetID = 140123456789
	in.Items = []model.OutfitItem{
		{AssetID: 121390054271388, Slot: "Hat", AssetType: "Hat", Price: 55},
		{AssetID: 429786881, Slot: "LeftArm", AssetType: "LeftArm", Price: 445,
			BundleID: 429785907, BundleName: "Blush Fashion Doll"},
		{AssetID: 429786958, Slot: "RightArm", AssetType: "RightArm", Price: 445,
			BundleID: 429785907, BundleName: "Blush Fashion Doll"},
	}

	created, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := svc.Get(context.Background(), created.OutfitID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ThumbnailAssetID != 140123456789 {
		t.Errorf("ThumbnailAssetID = %d, ingin 140123456789", got.ThumbnailAssetID)
	}

	bundled := 0
	for _, item := range got.Items {
		if item.BundleID == 429785907 {
			bundled++
			if item.BundleName != "Blush Fashion Doll" {
				t.Errorf("BundleName = %q, ingin %q", item.BundleName, "Blush Fashion Doll")
			}
		}
	}
	if bundled != 2 {
		t.Errorf("item ber-bundle = %d, ingin 2", bundled)
	}
}

// TestCreateOutfitMenolakBundleTidakValid memastikan validasi field bundle.
func TestCreateOutfitMenolakBundleTidakValid(t *testing.T) {
	svc := newOutfitService(t)

	cases := []struct {
		name string
		item model.OutfitItem
		code string
	}{
		{"bundleId negatif", model.OutfitItem{AssetID: 1, Slot: "Hat", BundleID: -1}, "invalid_bundle_id"},
		{"bundleName tanpa bundleId", model.OutfitItem{AssetID: 1, Slot: "Hat", BundleName: "Paket"}, "invalid_bundle_name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validCreateInput()
			in.Items = []model.OutfitItem{tc.item}
			_, err := svc.Create(context.Background(), in)
			requireAPIError(t, err, http.StatusUnprocessableEntity, tc.code)
		})
	}
}

// TestAccrueMenghitungBundleSekali memastikan cashback dihitung dari spend
// yang sudah men-dedup harga bundle: 3 bagian paket 445 + 1 item satuan 55
// menghasilkan spend 500, bukan 1390.
func TestAccrueMenghitungBundleSekali(t *testing.T) {
	f := newCashbackFixture()

	in := validTxInput("key-bundle-1")
	in.UserID = cbUser
	in.Items = []model.TransactionItem{
		{AssetID: 121390054271388, Price: 55, Result: model.ResultSuccess},
		{AssetID: 429786881, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
		{AssetID: 429786958, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
		{AssetID: 429787041, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
	}

	// Service transaksi memanggil Accrue sendiri, persis wiring produksi.
	if _, _, err := f.txs.Create(context.Background(), in); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	summary, err := f.cashback.Summary(context.Background(), cbUser)
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}

	// Spend benar = 500. Rate transaksi pertama: base 20 + first 10 = 30,
	// tanpa streak/event/loyal. Saldo = 500 * 30 / 100 = 150. Kalau bundle
	// terhitung 3x (spend 1390) saldo akan 417 — selisihnya jauh.
	if want := 500 * 30 / 100; summary.Balance != want {
		t.Errorf("Balance = %d, ingin %d (spend bundle dihitung sekali)", summary.Balance, want)
	}
}
