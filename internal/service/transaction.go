package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// CreateTransactionInput adalah muatan POST /v1/transactions.
type CreateTransactionInput struct {
	IdempotencyKey string
	UserID         int64
	UniverseID     int64
	PlaceID        int64
	JobID          string
	Status         string
	OccurredAt     time.Time
	Items          []model.TransactionItem
}

// TransactionPage adalah satu halaman riwayat transaksi.
type TransactionPage struct {
	Transactions []model.Transaction
	NextCursor   string
	HasMore      bool
}

// Transactions mencatat pembelian yang dilaporkan game server.
type Transactions struct {
	transactions store.Transactions
	now          func() time.Time
	newID        func() string
}

// NewTransactions merangkai service transaksi.
func NewTransactions(transactions store.Transactions) *Transactions {
	return &Transactions{
		transactions: transactions,
		now:          func() time.Time { return time.Now().UTC() },
		newID:        newTxID,
	}
}

// Create mencatat satu transaksi.
//
// Idempotensi ditegakkan lewat kolom unik idempotencyKey, bukan cache respons:
// kalau POST timeout padahal backend sudah menyimpan, retry dengan kunci yang
// sama mengembalikan transaksi lama dan replay bernilai true.
func (s *Transactions) Create(ctx context.Context, in CreateTransactionInput) (model.Transaction, bool, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return model.Transaction{}, false, apierr.BadRequest("missing_idempotency_key", "Header Idempotency-Key wajib untuk endpoint ini")
	}
	if existing, ok := s.transactions.ByIdempotencyKey(ctx, key); ok {
		return existing, true, nil
	}

	if in.UserID <= 0 {
		return model.Transaction{}, false, apierr.Unprocessable("missing_user_id", "Field userId wajib diisi")
	}
	if err := validateTxStatus(in.Status); err != nil {
		return model.Transaction{}, false, err
	}
	if len(in.Items) == 0 {
		return model.Transaction{}, false, apierr.Unprocessable("missing_items", "Transaksi wajib punya minimal satu item")
	}
	for i, item := range in.Items {
		if item.AssetID <= 0 {
			return model.Transaction{}, false, apierr.Unprocessable("invalid_asset_id", "Setiap item wajib punya assetId").
				WithDetails(map[string]any{"field": indexedField("items", i, "assetId")})
		}
		if item.Price < 0 {
			return model.Transaction{}, false, apierr.Unprocessable("invalid_price", "Harga tidak boleh negatif").
				WithDetails(map[string]any{"field": indexedField("items", i, "price")})
		}
		if err := validateItemResult(item.Result, i); err != nil {
			return model.Transaction{}, false, err
		}
	}

	now := s.now()
	if in.OccurredAt.IsZero() {
		in.OccurredAt = now
	}

	tx := model.Transaction{
		TxID:           s.newID(),
		UserID:         in.UserID,
		UniverseID:     in.UniverseID,
		PlaceID:        in.PlaceID,
		JobID:          in.JobID,
		IdempotencyKey: key,
		Status:         in.Status,
		OccurredAt:     in.OccurredAt.UTC(),
		ReceivedAt:     now,
		Items:          in.Items,
	}
	if err := s.transactions.Create(ctx, tx); err != nil {
		// Dua retry yang tiba bersamaan: yang kalah balapan membaca ulang
		// baris pemenang dan melaporkannya sebagai replay, bukan sebagai gagal.
		if errors.Is(err, store.ErrIdempotencyConflict) {
			if winner, ok := s.transactions.ByIdempotencyKey(ctx, key); ok {
				return winner, true, nil
			}
		}
		return model.Transaction{}, false, err
	}
	return tx, false, nil
}

// List mengembalikan riwayat transaksi pemain, terbaru dulu.
func (s *Transactions) List(ctx context.Context, userID int64, rawCursor string, limit int) (TransactionPage, error) {
	if userID <= 0 {
		return TransactionPage{}, apierr.BadRequest("missing_user_id", "Parameter userId wajib diisi")
	}

	var cursor paging.KeysetCursor
	present, err := paging.Decode(rawCursor, &cursor)
	if err != nil {
		return TransactionPage{}, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
	}
	after := &cursor
	if !present {
		after = nil
	}

	limit = paging.ClampLimit(limit, DefaultPageSize, MaxPageSize)
	rows, hasMore, err := s.transactions.ListByUser(ctx, userID, after, limit)
	if err != nil {
		return TransactionPage{}, err
	}

	page := TransactionPage{Transactions: rows, HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = paging.Encode(paging.KeysetCursor{At: last.OccurredAt, ID: last.TxID})
		if err != nil {
			return TransactionPage{}, err
		}
	}
	return page, nil
}

func validateTxStatus(status string) error {
	switch status {
	case "success", "partial", "failed", "aborted":
		return nil
	case "":
		return apierr.Unprocessable("missing_status", "Field status wajib diisi")
	default:
		return apierr.Unprocessable("invalid_status", "status harus salah satu dari success, partial, failed, aborted")
	}
}

func validateItemResult(result model.TxResult, index int) error {
	switch result {
	case model.ResultSuccess, model.ResultAborted, model.ResultFailed:
		return nil
	case "":
		return apierr.Unprocessable("missing_result", "Setiap item wajib punya result").
			WithDetails(map[string]any{"field": indexedField("items", index, "result")})
	default:
		return apierr.Unprocessable("invalid_result", "result harus salah satu dari success, aborted, failed").
			WithDetails(map[string]any{"field": indexedField("items", index, "result")})
	}
}

func indexedField(parent string, index int, child string) string {
	return fmt.Sprintf("%s[%d].%s", parent, index, child)
}
