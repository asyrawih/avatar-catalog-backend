package service_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

func newTxService() *service.Transactions {
	return service.NewTransactions(store.NewMemoryTransactions(), nil)
}

func validTxInput(key string) service.CreateTransactionInput {
	return service.CreateTransactionInput{
		IdempotencyKey: key,
		UserID:         seededUser,
		UniverseID:     8739264611,
		PlaceID:        124012682058672,
		JobID:          "b1f0e2c4-7a3d-4c11-9f88-2ab5c9e01d77",
		Status:         "success",
		OccurredAt:     time.Date(2026, 8, 4, 11, 22, 3, 0, time.UTC),
		Items: []model.TransactionItem{
			{AssetID: assetHair, Price: 69, Result: model.ResultSuccess},
			{AssetID: assetJacket, Price: 79, Result: model.ResultAborted},
		},
	}
}

func TestCreateTransactionHanyaMenghitungItemSukses(t *testing.T) {
	svc := newTxService()

	tx, replay, err := svc.Create(context.Background(), validTxInput("key-1"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if replay {
		t.Error("replay = true pada permintaan pertama")
	}
	if got := tx.RobuxTotal(); got != 69 {
		t.Errorf("robuxTotal = %d, ingin 69 (item aborted tidak dihitung)", got)
	}
	if tx.ReceivedAt.IsZero() {
		t.Error("receivedAt tidak diisi")
	}
}

func TestCreateTransactionMengulangKunciYangSama(t *testing.T) {
	svc := newTxService()
	ctx := context.Background()

	first, _, err := svc.Create(ctx, validTxInput("key-sama"))
	if err != nil {
		t.Fatalf("Create() pertama error = %v", err)
	}

	// Retry membawa muatan yang sama persis seperti yang dikirim ulang klien.
	second, replay, err := svc.Create(ctx, validTxInput("key-sama"))
	if err != nil {
		t.Fatalf("Create() kedua error = %v", err)
	}

	if !replay {
		t.Error("replay = false, ingin true untuk kunci yang sama")
	}
	if second.TxID != first.TxID {
		t.Errorf("txId = %q, ingin sama dengan %q", second.TxID, first.TxID)
	}
	if !second.ReceivedAt.Equal(first.ReceivedAt) {
		t.Error("receivedAt berubah saat replay")
	}
}

func TestCreateTransactionValidasi(t *testing.T) {
	tests := map[string]struct {
		mutate     func(*service.CreateTransactionInput)
		wantStatus int
		wantCode   string
	}{
		"tanpa idempotency key": {
			func(in *service.CreateTransactionInput) { in.IdempotencyKey = "  " },
			http.StatusBadRequest, "missing_idempotency_key",
		},
		"tanpa user": {
			func(in *service.CreateTransactionInput) { in.UserID = 0 },
			http.StatusUnprocessableEntity, "missing_user_id",
		},
		"status ngawur": {
			func(in *service.CreateTransactionInput) { in.Status = "selesai" },
			http.StatusUnprocessableEntity, "invalid_status",
		},
		"tanpa item": {
			func(in *service.CreateTransactionInput) { in.Items = nil },
			http.StatusUnprocessableEntity, "missing_items",
		},
		"result item ngawur": {
			func(in *service.CreateTransactionInput) { in.Items[0].Result = "entahlah" },
			http.StatusUnprocessableEntity, "invalid_result",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc := newTxService()
			in := validTxInput("key-" + name)
			tc.mutate(&in)

			_, _, err := svc.Create(context.Background(), in)
			requireAPIError(t, err, tc.wantStatus, tc.wantCode)
		})
	}
}

func TestListTransactionsTerbaruDulu(t *testing.T) {
	svc := newTxService()
	ctx := context.Background()

	lama := validTxInput("key-lama")
	lama.OccurredAt = time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	if _, _, err := svc.Create(ctx, lama); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	baru := validTxInput("key-baru")
	baru.OccurredAt = time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	if _, _, err := svc.Create(ctx, baru); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	page, err := svc.List(ctx, seededUser, "", 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Transactions) != 2 {
		t.Fatalf("jumlah transaksi = %d, ingin 2", len(page.Transactions))
	}
	if !page.Transactions[0].OccurredAt.After(page.Transactions[1].OccurredAt) {
		t.Error("urutan bukan terbaru dulu")
	}
}
