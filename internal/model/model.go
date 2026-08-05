// Package model berisi entitas inti. Struct di sini murni data — tanpa logika
// bisnis dan tanpa dependensi HTTP.
package model

import "time"

// TxResult adalah hasil pembelian satu item dalam sebuah transaksi.
type TxResult string

// Nilai TxResult yang valid.
const (
	ResultSuccess TxResult = "success"
	ResultAborted TxResult = "aborted"
	ResultFailed  TxResult = "failed"
)

// Player adalah pemain Roblox (tabel PLAYER).
type Player struct {
	UserID     int64
	UniverseID int64
	Username   string
	LastSeen   time.Time
}

// BodyTemplate adalah rig dasar yang dipakai outfit (tabel BODY_TEMPLATE).
//
// TemplateID adalah Roblox asset id dari rig yang sudah di-upload, disimpan
// sebagai string digit. Roblox yang memegang rig-nya; tabel ini hanya registry
// rig mana saja yang pernah dipakai, plus nama dan gender yang diisi belakangan.
type BodyTemplate struct {
	TemplateID    string
	Name          string
	Gender        string
	SourceAssetID int64
	CreatedAt     time.Time
}

// Outfit adalah set pakaian milik pemain (tabel OUTFIT).
//
// ReferenceID adalah UUID yang dikirim ke RecommendationService:RegisterItemAsync,
// sedangkan RecoItemID adalah itemId yang dikembalikan layanan tersebut dan
// dibutuhkan oleh RemoveItemAsync/UpdateItemAsync.
type Outfit struct {
	OutfitID    string
	ReferenceID string
	RecoItemID  *string
	UserID      int64
	TemplateID  string
	Name        string
	IsPublic    bool
	CustomTags  []string
	Items       []OutfitItem
	Body        *AvatarBody // nil = klien tidak melaporkannya
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time // soft delete: baris tidak pernah dihapus
}

// Deleted melaporkan apakah outfit sudah di-soft-delete.
func (o Outfit) Deleted() bool { return o.DeletedAt != nil }

// AvatarBody adalah bagian HumanoidDescription yang bukan asset: warna kulit
// per anggota badan dan skala tubuh.
//
// Item saja tidak cukup untuk merender ulang avatar — dua outfit dengan item
// identik tetap terlihat berbeda kalau warna dan skalanya beda. Nilainya
// dilaporkan klien dan disimpan apa adanya.
//
// Colors dan Scales masing-masing boleh nil: klien yang hanya melaporkan salah
// satu tidak dipaksa mengarang sisanya.
type AvatarBody struct {
	Colors *BodyColors
	Scales *BodyScales
}

// BodyColors adalah warna per anggota badan, hex RGB tanpa '#' (mis. "AE7C64").
type BodyColors struct {
	Head     string
	Torso    string
	LeftArm  string
	RightArm string
	LeftLeg  string
	RightLeg string
}

// BodyScales adalah skala tubuh dari HumanoidDescription.
//
// Height, Width, Head, dan Depth adalah pengali yang harus positif. BodyType
// dan Proportion adalah bobot yang boleh nol — nol memang nilai wajarnya.
type BodyScales struct {
	Height     float64
	Width      float64
	Head       float64
	Depth      float64
	BodyType   float64
	Proportion float64
}

// OutfitItem adalah satu asset yang terpasang pada slot outfit (tabel OUTFIT_ITEM).
//
// Name, AssetType, dan Price dilaporkan klien dari AvatarEditorService dan
// disimpan apa adanya — backend tidak punya katalog untuk memverifikasinya, dan
// ketiganya boleh kosong karena assetId plus slot sudah cukup untuk memasang item.
type OutfitItem struct {
	AssetID   int64
	Slot      string
	Name      string
	AssetType string
	Price     int
}

// Transaction adalah catatan pembelian dari game server (tabel TRANSACTION).
type Transaction struct {
	TxID           string
	UserID         int64
	UniverseID     int64
	PlaceID        int64
	JobID          string
	IdempotencyKey string
	Status         string
	OccurredAt     time.Time
	ReceivedAt     time.Time
	Items          []TransactionItem
}

// RobuxTotal menjumlahkan harga item yang benar-benar berhasil dibeli.
// Item aborted/failed tidak dihitung karena Robux-nya tidak berpindah.
func (t Transaction) RobuxTotal() int {
	var total int
	for _, item := range t.Items {
		if item.Result == ResultSuccess {
			total += item.Price
		}
	}
	return total
}

// TransactionItem adalah satu baris item dalam transaksi (tabel TRANSACTION_ITEM).
type TransactionItem struct {
	AssetID int64
	Price   int
	Result  TxResult
}
