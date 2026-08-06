package service_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

const cbUser = int64(900100200)

// cashbackFixture merangkai service transaksi + cashback di atas store memory
// yang sama, persis seperti wiring produksi.
type cashbackFixture struct {
	cashback *service.Cashback
	txs      *service.Transactions
	store    *store.MemoryCashback
}

func newCashbackFixture() cashbackFixture {
	txStore := store.NewMemoryTransactions()
	cbStore := store.NewMemoryCashback(txStore)
	cb := service.NewCashback(cbStore)
	return cashbackFixture{
		cashback: cb,
		txs:      service.NewTransactions(txStore, cb),
		store:    cbStore,
	}
}

// spend mencatat satu transaksi sukses lewat service transaksi, sehingga
// accrual ikut berjalan seperti di produksi.
func (f cashbackFixture) spend(t *testing.T, key string, amount int, occurredAt time.Time) model.Transaction {
	t.Helper()

	tx, _, err := f.txs.Create(context.Background(), service.CreateTransactionInput{
		IdempotencyKey: key,
		UserID:         cbUser,
		Status:         "success",
		OccurredAt:     occurredAt,
		Items:          []model.TransactionItem{{AssetID: 111222333, Price: amount, Result: model.ResultSuccess}},
	})
	if err != nil {
		t.Fatalf("Create transaksi: %v", err)
	}
	return tx
}

func (f cashbackFixture) balance(t *testing.T) int {
	t.Helper()
	balance, err := f.store.Balance(context.Background(), cbUser)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	return balance
}

func day(d int) time.Time {
	return time.Date(2026, 8, d, 13, 0, 0, 0, time.UTC)
}

func TestAccrualTransaksiPertamaDapatBonusFirst(t *testing.T) {
	f := newCashbackFixture()

	// Belum ada riwayat: dasar 20% + first 10% = 30%.
	f.spend(t, "key-1", 1000, day(10))

	if got := f.balance(t); got != 300 {
		t.Fatalf("balance = %d, mau 300 (30%% dari 1000)", got)
	}
}

func TestAccrualDasarDuaPuluhPersenDenganFloor(t *testing.T) {
	f := newCashbackFixture()
	f.spend(t, "key-1", 100, day(1)) // habiskan jatah first jauh-jauh hari

	f.spend(t, "key-2", 999, day(10))

	// 300 hari-1 tidak relevan: 100×30%=30; lalu floor(999×20%)=199.
	if got := f.balance(t); got != 30+199 {
		t.Fatalf("balance = %d, mau %d (floor 999×20%% = 199)", got, 30+199)
	}
}

func TestAccrualStreakDanLoyalDanEventKenaCap(t *testing.T) {
	f := newCashbackFixture()

	// Riwayat: spend besar (loyal) lalu dua hari beruntun sebelum transaksi.
	f.spend(t, "key-1", 6000, day(1))
	f.spend(t, "key-2", 100, day(8))
	f.spend(t, "key-3", 100, day(9))

	if _, err := f.cashback.CreateEvent(context.Background(), service.CreateEventInput{
		Name: "weekly", StartsAt: day(9), EndsAt: day(12),
	}); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	// Hari ketiga beruntun + event + loyal: 20+5+10+5 = 40 — pas di cap.
	rate, err := f.cashback.RateFor(context.Background(), cbUser, day(10), "")
	if err != nil {
		t.Fatalf("RateFor: %v", err)
	}
	if rate.Percent != service.MaxRatePercent {
		t.Fatalf("rate = %d%%, mau %d%%", rate.Percent, service.MaxRatePercent)
	}

	before := f.balance(t)
	f.spend(t, "key-4", 1000, day(10))
	if got := f.balance(t) - before; got != 400 {
		t.Fatalf("accrual = %d, mau 400 (cap 40%%)", got)
	}
}

func TestAccrualIdempotenLewatReplay(t *testing.T) {
	f := newCashbackFixture()

	f.spend(t, "key-1", 1000, day(10))
	first := f.balance(t)

	// Retry dengan kunci idempotensi sama: transaksi replay, cashback tidak ganda.
	f.spend(t, "key-1", 1000, day(10))
	if got := f.balance(t); got != first {
		t.Fatalf("balance setelah replay = %d, mau tetap %d", got, first)
	}
}

func TestRedeemDiBawahMinimumDitolak(t *testing.T) {
	f := newCashbackFixture()
	f.spend(t, "key-1", 100, day(10)) // cashback 30 — di bawah minimum 100

	_, err := f.cashback.Redeem(context.Background(), cbUser)
	assertAPIError(t, err, http.StatusUnprocessableEntity, "balance_below_minimum")
}

func TestRedeemMemotongSaldoDanMenolakPendingKedua(t *testing.T) {
	f := newCashbackFixture()
	f.spend(t, "key-1", 1000, day(10)) // saldo 300

	req, err := f.cashback.Redeem(context.Background(), cbUser)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if req.Amount != 300 || req.Status != model.RedeemPending {
		t.Fatalf("request = %+v, mau amount 300 status pending", req)
	}
	if got := f.balance(t); got != 0 {
		t.Fatalf("balance setelah redeem = %d, mau 0", got)
	}

	// Saldo baru masuk lagi, tapi request pending masih ada → ditolak.
	f.spend(t, "key-2", 1000, day(11))
	_, err = f.cashback.Redeem(context.Background(), cbUser)
	assertAPIError(t, err, http.StatusConflict, "redeem_pending_exists")
}

func TestResolveRedeemRejectedMengembalikanSaldo(t *testing.T) {
	f := newCashbackFixture()
	f.spend(t, "key-1", 1000, day(10))

	req, err := f.cashback.Redeem(context.Background(), cbUser)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}

	resolved, err := f.cashback.ResolveRedeem(context.Background(), req.RequestID, model.RedeemRejected)
	if err != nil {
		t.Fatalf("ResolveRedeem: %v", err)
	}
	if resolved.Status != model.RedeemRejected || resolved.ResolvedAt == nil {
		t.Fatalf("resolved = %+v, mau status rejected dengan resolvedAt terisi", resolved)
	}
	if got := f.balance(t); got != 300 {
		t.Fatalf("balance setelah ditolak = %d, mau 300 kembali", got)
	}

	// Resolusi ulang dengan status sama: no-op. Status berbeda: konflik.
	if _, err := f.cashback.ResolveRedeem(context.Background(), req.RequestID, model.RedeemRejected); err != nil {
		t.Fatalf("resolusi ulang status sama: %v", err)
	}
	_, err = f.cashback.ResolveRedeem(context.Background(), req.RequestID, model.RedeemCompleted)
	assertAPIError(t, err, http.StatusConflict, "redeem_already_resolved")
	if got := f.balance(t); got != 300 {
		t.Fatalf("balance setelah resolusi ulang = %d, mau tetap 300", got)
	}
}

func TestReversalMenarikCashbackDanIdempoten(t *testing.T) {
	f := newCashbackFixture()
	tx := f.spend(t, "key-1", 1000, day(10)) // saldo 300

	entry, replay, err := f.cashback.Reverse(context.Background(), tx.TxID)
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if replay || entry.Amount != -300 {
		t.Fatalf("entry = %+v replay=%v, mau amount -300 replay=false", entry, replay)
	}
	if got := f.balance(t); got != 0 {
		t.Fatalf("balance setelah reversal = %d, mau 0", got)
	}

	// Reversal kedua adalah replay, saldo tidak berubah lagi.
	_, replay, err = f.cashback.Reverse(context.Background(), tx.TxID)
	if err != nil {
		t.Fatalf("Reverse kedua: %v", err)
	}
	if !replay {
		t.Fatal("Reverse kedua mau replay=true")
	}
	if got := f.balance(t); got != 0 {
		t.Fatalf("balance = %d, mau tetap 0", got)
	}

	_, _, err = f.cashback.Reverse(context.Background(), "tx_tidakada")
	assertAPIError(t, err, http.StatusNotFound, "cashback_not_found")
}

func TestReconcileMenjumlahkanLedger(t *testing.T) {
	f := newCashbackFixture()
	tx := f.spend(t, "key-1", 1000, day(10)) // accrual 300
	f.spend(t, "key-2", 1000, day(11))       // accrual 200 (bukan first lagi)

	if _, _, err := f.cashback.Reverse(context.Background(), tx.TxID); err != nil {
		t.Fatalf("Reverse: %v", err)
	}

	// to eksplisit: default-nya now, dan entry yang lahir pada tick jam yang
	// sama akan jatuh tepat di batas interval half-open [from, to).
	report, err := f.cashback.Reconcile(context.Background(), time.Time{}, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	totals := report.Totals
	if totals.Accrued != 500 || totals.Reversed != 300 || totals.Outstanding != 200 {
		t.Fatalf("totals = %+v, mau accrued 500 reversed 300 outstanding 200", totals)
	}
}

func assertAPIError(t *testing.T, err error, status int, code string) {
	t.Helper()

	apiErr, ok := err.(*apierr.Error)
	if !ok {
		t.Fatalf("error = %v, mau *apierr.Error %s", err, code)
	}
	if apiErr.Status != status || apiErr.Code != code {
		t.Fatalf("error = %d %s, mau %d %s", apiErr.Status, apiErr.Code, status, code)
	}
}
