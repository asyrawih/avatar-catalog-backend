// Package store mendefinisikan port penyimpanan beserta implementasi
// in-memory-nya. Bentuk tiap port mengikuti tabelnya, jadi menukar implementasi
// ini dengan Postgres tidak menyentuh lapisan service.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
)

// ErrNotFound dikembalikan saat baris tidak ada.
var ErrNotFound = errors.New("store: baris tidak ditemukan")

// ErrConflict dikembalikan saat baris bentrok dengan yang sudah ada, mis.
// email operator dashboard yang sudah terpakai.
var ErrConflict = errors.New("store: baris sudah ada")

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
	// Sort menentukan urutan daftar. Zero value = terbaru dulu, urutan yang
	// berlaku sejak awal.
	Sort OutfitSort
}

// OutfitSort adalah urutan daftar outfit.
type OutfitSort string

// Nilai OutfitSort yang valid.
const (
	// SortRecent mengurutkan dari yang paling baru diperbarui.
	SortRecent OutfitSort = ""
	// SortMostLiked mengurutkan dari yang paling banyak disukai.
	SortMostLiked OutfitSort = "mostLiked"
	// SortMostViewed mengurutkan dari yang paling banyak dilihat.
	SortMostViewed OutfitSort = "mostViewed"
)

// ByCount melaporkan apakah urutan ini memakai pencacah, bukan waktu — yang
// menentukan jenis cursor mana yang dipakai.
func (s OutfitSort) ByCount() bool { return s == SortMostLiked || s == SortMostViewed }

// OutfitCursor menandai posisi halaman daftar outfit.
//
// Kuncinya ikut urutan yang diminta: urutan terbaru memakai (updatedAt,
// outfitId), urutan populer memakai (count, outfitId). Keduanya dibungkus satu
// tipe supaya List punya satu parameter cursor, dan tepat satu field yang
// terisi — pengisi cursor yang tidak cocok dengan Sort akan menyaring baris
// yang salah tanpa error yang kelihatan.
type OutfitCursor struct {
	Recency *paging.KeysetCursor
	Count   *paging.CountCursor
}

// EngagementCounts adalah keadaan popularitas outfit sesudah sebuah penulisan
// like atau view.
//
// Angkanya dibaca di dalam transaksi penulisan itu sendiri, bukan dihitung
// pemanggil dari pembacaan sebelumnya. Bedanya nyata saat cache baca aktif:
// pembacaan sebelumnya bisa dilayani entri lama, sehingga "nilai lama + 1"
// menghasilkan angka yang salah pada balasan aksi yang baru saja dilakukan
// klien.
type EngagementCounts struct {
	// Changed melaporkan apakah penulisan ini benar-benar mengubah sesuatu.
	// false pada like berulang dari pemain yang sama.
	Changed   bool
	LikeCount int
	ViewCount int
}

// Outfits menyimpan OUTFIT dan OUTFIT_ITEM.
type Outfits interface {
	// Create menyimpan outfit baru beserta itemnya.
	Create(ctx context.Context, o model.Outfit) error
	// CreateBatch menyimpan banyak outfit sekaligus dalam satu transaksi —
	// semua tersimpan atau tidak sama sekali. Bukan sekadar perulangan
	// Create: yang mahal pada impor massal bukan INSERT-nya melainkan
	// perjalanan bolak-balik per baris, dan di sini seluruh batch berangkat
	// sebagai satu rangkaian perintah.
	CreateBatch(ctx context.Context, outfits []model.Outfit) error
	// Get mengembalikan outfit termasuk yang sudah di-soft-delete; pemanggil
	// yang menentukan apakah itu 404 atau 410.
	Get(ctx context.Context, outfitID string) (model.Outfit, error)
	// List mengembalikan outfit hidup yang cocok dengan filter, dalam urutan
	// f.Sort. Cursor nil berarti halaman pertama.
	List(ctx context.Context, f OutfitFilter, after *OutfitCursor, limit int) ([]model.Outfit, bool, error)
	// ListByReferenceIDs mengembalikan outfit hidup untuk sekumpulan referenceId.
	ListByReferenceIDs(ctx context.Context, referenceIDs []string) ([]model.Outfit, error)
	// Update menerapkan fn pada outfit yang tersimpan lalu menyimpan hasilnya.
	Update(ctx context.Context, outfitID string, fn func(*model.Outfit) error) (model.Outfit, error)
	// Search mengembalikan outfit hidup terurut dari yang paling mirip dengan
	// f.Keyword — toleran salah ketik (trigram) dan, bila qEmbedding terisi,
	// juga mirip secara makna (cosine di embedding nama). qEmbedding nil
	// berarti pencarian leksikal saja; f.OutfitIDs diabaikan. Hasilnya
	// peringkat, bukan halaman, jadi tidak ada cursor.
	Search(ctx context.Context, f OutfitFilter, qEmbedding []float32, limit int) ([]model.Outfit, error)
	// SetNameEmbedding menyimpan embedding nama sebuah outfit. Dipanggil
	// asinkron setelah create/rename; kegagalannya tidak menggagalkan
	// penulisan outfit itu sendiri.
	SetNameEmbedding(ctx context.Context, outfitID string, embedding []float32) error

	// Like mencatat bahwa userID menyukai outfit dan menaikkan like_count.
	// Idempoten: like kedua dari pemain yang sama tidak mengubah apa pun,
	// karena yang menegakkan keunikan adalah kunci primer tabel, bukan
	// pemeriksaan di aplikasi yang bisa kalah balapan.
	Like(ctx context.Context, outfitID string, userID int64, at time.Time) (EngagementCounts, error)
	// Unlike menghapus like dan menurunkan like_count. Changed false bila
	// pemain memang belum pernah menyukainya.
	Unlike(ctx context.Context, outfitID string, userID int64) (EngagementCounts, error)
	// RecordView menambahkan satu baris kejadian lihat dan menaikkan
	// view_count. Selalu menulis baris baru: berapa kali sebuah outfit dilihat
	// sebelum disukai adalah bagian dari sinyal yang mau disimpan. userID 0
	// berarti penonton anonim.
	RecordView(ctx context.Context, outfitID string, userID int64, at time.Time) (EngagementCounts, error)
	// Liked melaporkan outfit mana saja dari outfitIDs yang sudah disukai
	// userID. Dipakai daftar untuk mengisi penanda "sudah kamu suka" tanpa
	// satu query per baris.
	Liked(ctx context.Context, userID int64, outfitIDs []string) (map[string]bool, error)
}

// APIKeys menyimpan API_KEY — kunci milik konsumen backend.
type APIKeys interface {
	// ByKeyID mengembalikan kunci beserta hash-nya, termasuk yang sudah
	// dicabut atau kedaluwarsa: pemanggil yang memutuskan, supaya alasan
	// penolakan bisa dicatat tanpa membocorkannya ke klien.
	ByKeyID(ctx context.Context, keyID string) (auth.Key, error)
	// Create menyimpan kunci baru.
	Create(ctx context.Context, key auth.Key) error
	// List mengembalikan seluruh kunci, terbaru dulu. Untuk peninjauan, bukan
	// jalur request.
	List(ctx context.Context) ([]auth.Key, error)
	// Revoke menandai kunci dicabut. Idempoten: mencabut kunci yang sudah
	// dicabut bukan error, karena hasil akhirnya sama.
	Revoke(ctx context.Context, keyID string, at time.Time) error
	// Update mengubah nama dan/atau masa berlaku kunci. Rahasianya tidak ikut
	// berubah — kunci yang sama tetap berlaku dengan atribut baru.
	Update(ctx context.Context, keyID string, name string, expiresAt *time.Time) error
	// Delete menghapus baris kunci sepenuhnya. Berbeda dari Revoke, yang
	// menyisakan jejak: pemakaian terakhir dan alasan pencabutan ikut hilang.
	Delete(ctx context.Context, keyID string) error
	// TouchLastUsed mencatat pemakaian terakhir. Kegagalannya tidak boleh
	// menggagalkan request — ini catatan operasional, bukan bagian keputusan
	// autentikasi.
	TouchLastUsed(ctx context.Context, keyID string, at time.Time) error
}

// DashboardUsers menyimpan operator dashboard beserta sesi loginnya.
//
// Sesi ikut di sini, bukan di interface sendiri, karena keduanya selalu
// dipakai bersama dalam satu alur: login membaca user lalu membuat sesi, dan
// tiap request membaca sesi lalu user-nya.
type DashboardUsers interface {
	// ByEmail mencari user untuk login. Email dibandingkan huruf kecil.
	ByEmail(ctx context.Context, email string) (auth.User, error)
	// ByID mengembalikan user, termasuk yang sudah dinonaktifkan.
	ByID(ctx context.Context, userID string) (auth.User, error)
	// Create menyimpan operator baru.
	Create(ctx context.Context, user auth.User) error
	// List mengembalikan seluruh operator, terbaru dulu.
	List(ctx context.Context) ([]auth.User, error)
	// SetDisabled menonaktifkan atau mengaktifkan kembali operator. at nil
	// berarti diaktifkan lagi.
	SetDisabled(ctx context.Context, userID string, at *time.Time) error
	// SetPassword mengganti hash kata sandi.
	SetPassword(ctx context.Context, userID, passwordHash string) error
	// UpdateProfile mengganti email dan nama tampilan operator. Email yang
	// sudah dipakai operator lain mengembalikan ErrConflict.
	UpdateProfile(ctx context.Context, userID, email, name string) error
	// Delete menghapus operator beserta seluruh sesinya.
	Delete(ctx context.Context, userID string) error
	// TouchLastLogin mencatat login terakhir. Kegagalannya tidak boleh
	// menggagalkan login — ini catatan operasional.
	TouchLastLogin(ctx context.Context, userID string, at time.Time) error

	// CreateSession menyimpan sesi baru.
	CreateSession(ctx context.Context, session auth.Session) error
	// SessionByID mengembalikan sesi apa adanya, termasuk yang dicabut atau
	// kedaluwarsa: pemanggil yang memutuskan, supaya alasan penolakan bisa
	// dicatat tanpa membocorkannya ke klien.
	SessionByID(ctx context.Context, sessionID string) (auth.Session, error)
	// RevokeSession mematikan satu sesi. Idempoten.
	RevokeSession(ctx context.Context, sessionID string, at time.Time) error
	// RevokeUserSessions mematikan seluruh sesi milik satu user — dipakai saat
	// operator dinonaktifkan atau kata sandinya diganti.
	RevokeUserSessions(ctx context.Context, userID string, at time.Time) error
	// TouchSession mencatat pemakaian terakhir sesi.
	TouchSession(ctx context.Context, sessionID string, at time.Time) error
}

// Transactions menyimpan TRANSACTION dan TRANSACTION_ITEM.
type Transactions interface {
	// Create menyimpan transaksi baru.
	Create(ctx context.Context, tx model.Transaction) error
	// ByIdempotencyKey mencari transaksi yang sudah tercatat untuk kunci yang sama.
	ByIdempotencyKey(ctx context.Context, key string) (model.Transaction, bool)
	// ListByUser mengembalikan riwayat transaksi pemain, terbaru dulu.
	ListByUser(ctx context.Context, userID int64, after *paging.KeysetCursor, limit int) ([]model.Transaction, bool, error)
	// List mengembalikan transaksi SELURUH pemain, terbaru dulu. Dipakai
	// dashboard internal yang memantau arus transaksi apa adanya, bukan
	// riwayat satu pemain.
	List(ctx context.Context, after *paging.KeysetCursor, limit int) ([]model.Transaction, bool, error)
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
