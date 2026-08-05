// Package store mendefinisikan port penyimpanan beserta implementasi
// in-memory-nya. Bentuk tiap port mengikuti tabelnya, jadi menukar implementasi
// ini dengan Postgres tidak menyentuh lapisan service.
package store

import (
	"context"
	"errors"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
)

// ErrNotFound dikembalikan saat baris tidak ada.
var ErrNotFound = errors.New("store: baris tidak ditemukan")

// ErrIdempotencyConflict dikembalikan saat penulisan kalah balapan dengan
// permintaan lain yang memakai kunci idempotensi sama. Pemanggil sebaiknya
// membaca ulang baris pemenang, bukan menganggapnya gagal.
var ErrIdempotencyConflict = errors.New("store: kunci idempotensi sudah dipakai")

// OutfitFilter menyaring daftar outfit. Field kosong berarti tanpa penyaringan,
// sehingga satu query bisa melayani daftar milik satu pemain maupun daftar
// lintas pemain.
type OutfitFilter struct {
	UserID   int64 // 0 = semua pemain
	IsPublic *bool // nil = publik dan privat
	// OutfitIDs membatasi daftar ke sekumpulan outfitId tertentu; kosong =
	// semua outfit. Berguna untuk mengambil beberapa outfit sekaligus tanpa
	// satu GET detail per id.
	OutfitIDs []string
	// Keyword mencocokkan sebagian nama outfit tanpa peduli huruf besar-kecil;
	// kosong = tanpa pencarian.
	Keyword string
}

// Outfits menyimpan OUTFIT dan OUTFIT_ITEM.
type Outfits interface {
	// Create menyimpan outfit baru beserta itemnya.
	Create(ctx context.Context, o model.Outfit) error
	// Get mengembalikan outfit termasuk yang sudah di-soft-delete; pemanggil
	// yang menentukan apakah itu 404 atau 410.
	Get(ctx context.Context, outfitID string) (model.Outfit, error)
	// List mengembalikan outfit hidup yang cocok dengan filter, terbaru dulu.
	List(ctx context.Context, f OutfitFilter, after *paging.KeysetCursor, limit int) ([]model.Outfit, bool, error)
	// ListByReferenceIDs mengembalikan outfit hidup untuk sekumpulan referenceId.
	ListByReferenceIDs(ctx context.Context, referenceIDs []string) ([]model.Outfit, error)
	// Update menerapkan fn pada outfit yang tersimpan lalu menyimpan hasilnya.
	Update(ctx context.Context, outfitID string, fn func(*model.Outfit) error) (model.Outfit, error)
}

// Transactions menyimpan TRANSACTION dan TRANSACTION_ITEM.
type Transactions interface {
	// Create menyimpan transaksi baru.
	Create(ctx context.Context, tx model.Transaction) error
	// ByIdempotencyKey mencari transaksi yang sudah tercatat untuk kunci yang sama.
	ByIdempotencyKey(ctx context.Context, key string) (model.Transaction, bool)
	// ListByUser mengembalikan riwayat transaksi pemain, terbaru dulu.
	ListByUser(ctx context.Context, userID int64, after *paging.KeysetCursor, limit int) ([]model.Transaction, bool, error)
}

// Templates menyimpan BODY_TEMPLATE — registry rig yang sudah di-upload ke Roblox.
type Templates interface {
	// Get mengembalikan rig dasar berdasarkan templateId (Roblox asset id).
	Get(ctx context.Context, templateID string) (model.BodyTemplate, error)
	// Ensure mendaftarkan rig bila belum ada, lalu mengembalikan baris yang
	// berlaku beserta penanda apakah baris itu baru dibuat. Rig yang sudah
	// terdaftar tidak ditimpa: nama dan gender yang sudah diisi lewat PATCH
	// tidak boleh hilang hanya karena outfit baru memakai rig yang sama.
	Ensure(ctx context.Context, tpl model.BodyTemplate) (model.BodyTemplate, bool, error)
	// List mengembalikan rig terdaftar, terbaru dulu.
	List(ctx context.Context, after *paging.KeysetCursor, limit int) ([]model.BodyTemplate, bool, error)
	// Update menerapkan fn pada rig tersimpan lalu menyimpan hasilnya.
	Update(ctx context.Context, templateID string, fn func(*model.BodyTemplate) error) (model.BodyTemplate, error)
}
