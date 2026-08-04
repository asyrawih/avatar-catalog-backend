package store

import (
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
)

// SeedData mengisi penyimpanan in-memory dengan data contoh yang sama persis
// dengan mock Postman, supaya `go run ./cmd/server` langsung bisa dipakai
// menjalankan collection tanpa setup apa pun.
//
// Fungsi ini hanya untuk pengembangan.
func SeedData(templates *MemoryTemplates, outfits *MemoryOutfits) {
	seedTemplates(templates)
	seedOutfits(outfits)
}

// DevRigTemplateID adalah rig development yang sudah di-upload ke Roblox dan
// dipakai data contoh. Rig lain tidak diarang di sini — begitu dipakai lewat
// POST /v1/outfits, barisnya terdaftar sendiri.
const DevRigTemplateID = "88484288792766"

func seedTemplates(t *MemoryTemplates) {
	tpl := model.BodyTemplate{
		TemplateID:    DevRigTemplateID,
		Name:          "Dev Rig",
		Gender:        "?",
		SourceAssetID: 88484288792766,
		CreatedAt:     time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
	}
	t.rows[tpl.TemplateID] = tpl
}

func seedOutfits(o *MemoryOutfits) {
	recoItemID := "reco_7b31c9"

	y2k := model.Outfit{
		OutfitID:    "otf_9f2a41",
		ReferenceID: "550e8400-e29b-41d4-a716-446655440000",
		RecoItemID:  &recoItemID,
		UserID:      627278822,
		TemplateID:  DevRigTemplateID,
		Name:        "Y2K Streetwear",
		IsPublic:    true,
		CustomTags:  []string{"category:y2k", "gender:male"},
		Items: []model.OutfitItem{
			{AssetID: 78872304386489, Slot: "Hair", Name: "BLOND BARREL TWISTS DREADS", AssetType: "HairAccessory", Price: 69},
			{AssetID: 14433369343, Slot: "Jacket", Name: "Hero Jacket Oni Blood Moon", AssetType: "Accessory", Price: 79},
			{AssetID: 116123466288288, Slot: "Face", Name: "Carter Shades w Goatee", AssetType: "FaceAccessory", Price: 99},
		},
		CreatedAt: time.Date(2026, 7, 28, 9, 12, 44, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 4, 11, 3, 19, 0, time.UTC),
	}

	girly := model.Outfit{
		OutfitID:    "otf_3c88de",
		ReferenceID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		UserID:      627278822,
		TemplateID:  DevRigTemplateID,
		Name:        "Girly Pop Casual",
		IsPublic:    false,
		CustomTags:  []string{"category:doll", "gender:female"},
		Items: []model.OutfitItem{
			{AssetID: 120044550011, Slot: "Hair", Name: "Pink Bow Twin Tails", AssetType: "HairAccessory", Price: 55},
			{AssetID: 120044550012, Slot: "Face", Name: "Sugar Heart Blush", AssetType: "FaceAccessory", Price: 35},
			{AssetID: 120044550013, Slot: "Jacket", Name: "Frill Cardigan", AssetType: "Accessory", Price: 85},
			{AssetID: 120044550014, Slot: "Pants", Name: "Pastel Pleated Skirt", AssetType: "Pants", Price: 45},
			{AssetID: 120044550015, Slot: "Shoes", Name: "Doll Mary Janes", AssetType: "Accessory", Price: 60},
			{AssetID: 120044550016, Slot: "Back", Name: "Chibi Star Backpack", AssetType: "Accessory", Price: 40},
		},
		CreatedAt: time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 1, 18, 40, 2, 0, time.UTC),
	}

	o.rows[y2k.OutfitID] = y2k
	o.rows[girly.OutfitID] = girly
}
