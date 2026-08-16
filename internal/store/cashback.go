package store

import (
	"context"
	"errors"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
)

// ErrDuplicateEntry dikembalikan saat ledger sudah punya baris untuk pasangan
// (txId, kind) yang sama. Pemanggil memperlakukannya sebagai replay, bukan gagal.
var ErrDuplicateEntry = errors.New("store: entry cashback untuk transaksi ini sudah ada")

// ErrPendingRedeemExists dikembalikan saat pemain masih punya request redeem
// berstatus pending — spec membatasi maksimal satu.
var ErrPendingRedeemExists = errors.New("store: masih ada request redeem pending")

// SpendStats merangkum riwayat belanja sukses pemain dari TRANSACTION +
// TRANSACTION_ITEM. Hanya item result='success' yang dihitung, karena hanya
// itu yang Robux-nya benar-benar berpindah.
type SpendStats struct {
	// LifetimeSpend adalah total harga item sukses sepanjang masa.
	LifetimeSpend int
	// PurchaseCount adalah jumlah transaksi yang punya spend sukses > 0.
	PurchaseCount int
	// SpendDays berisi tanggal UTC (format "2006-01-02") dengan spend sukses,
	// dibatasi sejak daysSince — cukup untuk memeriksa streak.
	SpendDays map[string]bool
}

// UserBalance adalah ringkasan cashback satu pemain untuk daftar lintas
// pemain. Rate berjalan sengaja TIDAK ada di sini: menghitungnya butuh riwayat
// belanja tiap pemain, satu query per baris, dan angka itu hanya berarti saat
// sebuah transaksi sedang dinilai — bukan sebagai kolom tabel.
type UserBalance struct {
	UserID int64
	// Balance boleh minus setelah reversal.
	Balance int
	// Accrued dan Redeemed sepanjang masa, keduanya positif.
	Accrued  int
	Redeemed int
	// Reversed sepanjang masa, positif: berapa yang ditarik kembali.
	Reversed    int
	EntryCount  int
	LastEntryAt time.Time
}

// RedeemFilter menyaring daftar request redeem. Field kosong = tanpa saringan,
// jadi satu query melayani riwayat pemain maupun antrean fulfillment internal.
type RedeemFilter struct {
	UserID int64  // 0 = semua pemain
	Status string // "" = semua status
}

// Cashback menyimpan CASHBACK_ENTRY, REDEEM_REQUEST, dan CASHBACK_EVENT.
type Cashback interface {
	// SpendStats membaca riwayat belanja sukses pemain. excludeTxID membuang
	// transaksi yang sedang di-accrue supaya bonus dihitung dari riwayat
	// SEBELUM transaksi itu; kosong = tanpa pengecualian.
	SpendStats(ctx context.Context, userID int64, excludeTxID string, daysSince time.Time) (SpendStats, error)

	// CreateEntry menambah satu baris ledger. Pasangan (txId, kind) yang sudah
	// ada mengembalikan ErrDuplicateEntry.
	CreateEntry(ctx context.Context, e model.CashbackEntry) error
	// EntryByTxKind mencari baris ledger sebuah transaksi untuk kind tertentu.
	EntryByTxKind(ctx context.Context, txID string, kind model.CashbackKind) (model.CashbackEntry, bool, error)
	// Balance menjumlahkan seluruh amount pemain. Boleh minus setelah reversal.
	Balance(ctx context.Context, userID int64) (int, error)
	// ListEntries mengembalikan ledger pemain, terbaru dulu.
	ListEntries(ctx context.Context, userID int64, after *paging.KeysetCursor, limit int) ([]model.CashbackEntry, bool, error)
	// ListBalances merangkum ledger PER PEMAIN, aktivitas terbaru dulu.
	//
	// Diagregasi di penyimpanan, bukan dengan memanggil Balance sekali per
	// pemain dari service: yang kedua berarti satu query per baris daftar, dan
	// jumlah pemain hanya akan bertambah.
	ListBalances(ctx context.Context, after *paging.KeysetCursor, limit int) ([]UserBalance, bool, error)

	// CreateRedeem menyimpan request pending sekaligus baris ledger pemotong
	// saldo dalam satu kesatuan. Pemain yang masih punya request pending
	// mendapat ErrPendingRedeemExists.
	CreateRedeem(ctx context.Context, r model.RedeemRequest, deduct model.CashbackEntry) error
	// GetRedeem mengembalikan satu request redeem.
	GetRedeem(ctx context.Context, requestID string) (model.RedeemRequest, error)
	// ResolveRedeem menerapkan fn pada request tersimpan lalu menyimpan
	// hasilnya; bila fn mengembalikan entry (mis. pengembalian saldo saat
	// ditolak), entry ikut ditulis dalam kesatuan yang sama. fn yang
	// mengembalikan error membatalkan seluruh penulisan.
	ResolveRedeem(ctx context.Context, requestID string, fn func(*model.RedeemRequest) (*model.CashbackEntry, error)) (model.RedeemRequest, error)
	// ListRedeems mengembalikan request yang cocok dengan filter, terbaru dulu.
	ListRedeems(ctx context.Context, f RedeemFilter, after *paging.KeysetCursor, limit int) ([]model.RedeemRequest, bool, error)

	// CreateEvent menjadwalkan satu jendela bonus event.
	CreateEvent(ctx context.Context, ev model.CashbackEvent) error
	// ActiveEvent mencari event yang jendelanya mencakup at.
	ActiveEvent(ctx context.Context, at time.Time) (model.CashbackEvent, bool, error)
	// ListEvents mengembalikan event yang belum berakhir pada from, urut mulai.
	ListEvents(ctx context.Context, from time.Time) ([]model.CashbackEvent, error)
	// EventByID mengembalikan satu event. ErrNotFound bila tidak ada.
	EventByID(ctx context.Context, eventID string) (model.CashbackEvent, error)
	// UpdateEvent menimpa nama dan jendela sebuah event.
	UpdateEvent(ctx context.Context, ev model.CashbackEvent) error
	// DeleteEvent menghapus event. Pemanggil yang memastikan event itu belum
	// mulai — baris yang jendelanya sudah lewat adalah bagian dari penjelasan
	// rate yang terlanjur diberikan.
	DeleteEvent(ctx context.Context, eventID string) error

	// Totals mengagregat ledger pada rentang [from, to) untuk rekonsiliasi.
	Totals(ctx context.Context, from, to time.Time) (model.CashbackTotals, error)
}
