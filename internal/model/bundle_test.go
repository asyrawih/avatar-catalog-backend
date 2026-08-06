package model_test

import (
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/model"
)

// TestRobuxTotalMenghitungBundleSekali memastikan harga bundle induk yang
// terulang di tiap bagian paket hanya dihitung sekali per bundleId — angka ini
// dasar accrue cashback, jadi salah hitung langsung membengkakkan saldo.
func TestRobuxTotalMenghitungBundleSekali(t *testing.T) {
	tx := model.Transaction{Items: []model.TransactionItem{
		// Item satuan biasa.
		{AssetID: 121390054271388, Price: 55, Result: model.ResultSuccess},
		// Tiga bagian dari bundle yang sama, harga bundle 445 terulang 3x.
		{AssetID: 429786881, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
		{AssetID: 429786958, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
		{AssetID: 429787041, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
		// Dua bagian dari bundle lain, harga 250 terulang 2x.
		{AssetID: 619528412, Price: 250, Result: model.ResultSuccess, BundleID: 928374650},
		{AssetID: 619528641, Price: 250, Result: model.ResultSuccess, BundleID: 928374650},
		// Item gagal tidak dihitung, bundle maupun bukan.
		{AssetID: 111, Price: 99, Result: model.ResultFailed},
		{AssetID: 222, Price: 300, Result: model.ResultAborted, BundleID: 555},
	}}

	want := 55 + 445 + 250
	if got := tx.RobuxTotal(); got != want {
		t.Errorf("RobuxTotal() = %d, ingin %d (bundle dihitung sekali per bundleId)", got, want)
	}
}

// TestRobuxTotalBundleGagalSebagian: bagian pertama bundle gagal, bagian kedua
// sukses — harga bundle tetap dihitung sekali dari bagian yang sukses.
func TestRobuxTotalBundleGagalSebagian(t *testing.T) {
	tx := model.Transaction{Items: []model.TransactionItem{
		{AssetID: 1, Price: 445, Result: model.ResultFailed, BundleID: 429785907},
		{AssetID: 2, Price: 445, Result: model.ResultSuccess, BundleID: 429785907},
	}}

	if got := tx.RobuxTotal(); got != 445 {
		t.Errorf("RobuxTotal() = %d, ingin 445", got)
	}
}
