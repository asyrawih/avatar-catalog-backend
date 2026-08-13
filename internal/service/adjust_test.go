package service_test

import (
	"context"
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/model"
)

// TestCreateOutfitMembawaFieldAdjust memastikan koreksi penempatan per item
// tersimpan dan terbaca kembali apa adanya — termasuk membedakan komponen yang
// tidak dilaporkan (nil) dari komponen yang dilaporkan nol.
func TestCreateOutfitMembawaFieldAdjust(t *testing.T) {
	svc := newOutfitService(t)

	in := validCreateInput()
	in.Items = []model.OutfitItem{
		{AssetID: 16770389930, Slot: "Face", AssetType: "FaceAccessory", Price: 86,
			Adjust: &model.ItemAdjust{Pos: &model.Vec3{Y: -0.3}}},
		{AssetID: 13260631087, Slot: "Hair", AssetType: "HairAccessory", Price: 59},
	}

	created, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := svc.Get(context.Background(), created.OutfitID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	byAsset := make(map[int64]model.OutfitItem, len(got.Items))
	for _, item := range got.Items {
		byAsset[item.AssetID] = item
	}

	face := byAsset[16770389930]
	if face.Adjust == nil {
		t.Fatal("Adjust item Face = nil, ingin terisi")
	}
	if face.Adjust.Pos == nil {
		t.Fatal("Adjust.Pos item Face = nil, ingin terisi")
	}
	if want := (model.Vec3{Y: -0.3}); *face.Adjust.Pos != want {
		t.Errorf("Adjust.Pos = %+v, ingin %+v", *face.Adjust.Pos, want)
	}
	// rot dan scale tidak dilaporkan: keduanya harus tetap nil, bukan Vec3 nol.
	if face.Adjust.Rot != nil || face.Adjust.Scale != nil {
		t.Errorf("Adjust.Rot/Scale = %+v/%+v, ingin dua-duanya nil",
			face.Adjust.Rot, face.Adjust.Scale)
	}

	if hair := byAsset[13260631087]; hair.Adjust != nil {
		t.Errorf("Adjust item Hair = %+v, ingin nil", hair.Adjust)
	}
}
