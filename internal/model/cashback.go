package model

import "time"

// CashbackKind membedakan arah dan asal satu baris ledger cashback.
type CashbackKind string

// Nilai CashbackKind yang valid.
const (
	// CashbackAccrual adalah cashback yang lahir dari satu transaksi sukses.
	CashbackAccrual CashbackKind = "accrual"
	// CashbackReversal menarik kembali cashback transaksi yang di-refund.
	CashbackReversal CashbackKind = "reversal"
	// CashbackRedeem memotong saldo saat request redeem dibuat.
	CashbackRedeem CashbackKind = "redeem"
	// CashbackRedeemReturn mengembalikan potongan saat request ditolak.
	CashbackRedeemReturn CashbackKind = "redeem_return"
)

// CashbackEntry adalah satu baris ledger cashback (tabel CASHBACK_ENTRY).
//
// Ledger append-only: saldo pemain adalah jumlah Amount seluruh barisnya.
// Amount bertanda — accrual dan redeem_return positif, reversal dan redeem
// negatif. Semua angka dalam Robux.
type CashbackEntry struct {
	EntryID   string
	UserID    int64
	TxID      string // accrual/reversal; kosong untuk baris redeem
	RequestID string // redeem/redeem_return; kosong untuk baris transaksi
	Kind      CashbackKind
	// Spend dan RatePercent hanya terisi pada accrual, disimpan supaya baris
	// bisa dijelaskan tanpa menghitung ulang bonus yang berlaku saat itu.
	Spend       int
	RatePercent int
	Amount      int
	CreatedAt   time.Time
}

// Status request redeem yang valid.
const (
	RedeemPending   = "pending"
	RedeemCompleted = "completed"
	RedeemRejected  = "rejected"
)

// RedeemRequest adalah permintaan pencairan saldo (tabel REDEEM_REQUEST).
//
// Saldo dipotong saat request dibuat; fulfillment dikerjakan tim internal di
// luar sistem ini dan hasilnya masuk lewat perubahan status.
type RedeemRequest struct {
	RequestID   string
	UserID      int64
	Amount      int
	Status      string
	RequestedAt time.Time
	ResolvedAt  *time.Time // nil selama masih pending
}

// CashbackEvent adalah satu jendela waktu saat bonus event aktif
// (tabel CASHBACK_EVENT).
type CashbackEvent struct {
	EventID  string
	Name     string
	StartsAt time.Time
	EndsAt   time.Time
}

// Active melaporkan apakah jendela event mencakup waktu at.
func (e CashbackEvent) Active(at time.Time) bool {
	return !at.Before(e.StartsAt) && at.Before(e.EndsAt)
}

// CashbackTotals adalah agregat ledger untuk rekonsiliasi. Accrued, Reversed,
// Redeemed, dan Returned dihitung dalam rentang yang diminta; Outstanding
// adalah saldo bersih seluruh pemain sepanjang masa (kewajiban berjalan).
// Semua nilai positif kecuali Outstanding yang mengikuti tanda saldo.
type CashbackTotals struct {
	Accrued     int
	Reversed    int
	Redeemed    int
	Returned    int
	Outstanding int
}
