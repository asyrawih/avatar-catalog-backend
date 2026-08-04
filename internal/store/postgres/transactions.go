package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Transactions adalah implementasi store.Transactions di atas Postgres.
type Transactions struct {
	pool *pgxpool.Pool
}

// NewTransactions merangkai penyimpanan transaksi.
func NewTransactions(pool *pgxpool.Pool) *Transactions { return &Transactions{pool: pool} }

var _ store.Transactions = (*Transactions)(nil)

const txColumns = `
	tx_id, user_id, universe_id, place_id, job_id, idempotency_key,
	status, occurred_at, received_at`

// Create menyimpan transaksi beserta itemnya dalam satu transaksi database.
//
// Batasan unik pada idempotency_key yang menjadi penjaga sesungguhnya: kalau
// dua retry tiba bersamaan, salah satunya kalah dan mendapat
// store.ErrIdempotencyConflict, bukan baris kedua.
func (s *Transactions) Create(ctx context.Context, tx model.Transaction) error {
	err := s.inTx(ctx, func(dbTx pgx.Tx) error {
		if err := ensurePlayer(ctx, dbTx, tx.UserID); err != nil {
			return err
		}

		_, err := dbTx.Exec(ctx, `
			INSERT INTO transaction (tx_id, user_id, universe_id, place_id, job_id,
			                         idempotency_key, status, occurred_at, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			tx.TxID, tx.UserID, nullableInt64(tx.UniverseID), nullableInt64(tx.PlaceID),
			nullableString(tx.JobID), tx.IdempotencyKey, tx.Status, tx.OccurredAt, tx.ReceivedAt)
		if err != nil {
			return err
		}

		batch := &pgx.Batch{}
		for _, item := range tx.Items {
			batch.Queue(`
				INSERT INTO transaction_item (tx_id, asset_id, price, result)
				VALUES ($1, $2, $3, $4)`, tx.TxID, item.AssetID, item.Price, item.Result)
		}

		results := dbTx.SendBatch(ctx, batch)
		defer results.Close()

		for range tx.Items {
			if _, err := results.Exec(); err != nil {
				return err
			}
		}
		return nil
	})

	if isUniqueViolation(err, "transaction_idempotency_key_key") {
		return store.ErrIdempotencyConflict
	}
	return err
}

// ByIdempotencyKey mencari transaksi yang sudah tercatat untuk kunci yang sama.
func (s *Transactions) ByIdempotencyKey(ctx context.Context, key string) (model.Transaction, bool) {
	row := s.pool.QueryRow(ctx, `SELECT`+txColumns+` FROM transaction WHERE idempotency_key = $1`, key)

	tx, err := scanTransaction(row)
	if err != nil {
		// Termasuk pgx.ErrNoRows: kunci belum pernah dipakai.
		return model.Transaction{}, false
	}
	if err := s.attachItems(ctx, []*model.Transaction{&tx}); err != nil {
		return model.Transaction{}, false
	}
	return tx, true
}

// ListByUser mengembalikan satu halaman riwayat transaksi pemain.
func (s *Transactions) ListByUser(ctx context.Context, userID int64, after *paging.KeysetCursor, limit int) ([]model.Transaction, bool, error) {
	var (
		cursorAt time.Time
		cursorID string
	)
	if after != nil {
		cursorAt, cursorID = after.At, after.ID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT`+txColumns+`
		FROM transaction
		WHERE user_id = $1
		  AND ($2::timestamptz IS NULL
		       OR occurred_at < $2
		       OR (occurred_at = $2 AND tx_id > $3))
		ORDER BY occurred_at DESC, tx_id ASC
		LIMIT $4`,
		userID, nullableTime(after, cursorAt), cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}

	transactions, err := collectTransactions(rows)
	if err != nil {
		return nil, false, err
	}

	transactions, hasMore := trimPage(transactions, limit)

	refs := make([]*model.Transaction, 0, len(transactions))
	for i := range transactions {
		refs = append(refs, &transactions[i])
	}
	if err := s.attachItems(ctx, refs); err != nil {
		return nil, false, err
	}
	return transactions, hasMore, nil
}

// attachItems mengisi Items untuk sekumpulan transaksi dengan satu query.
func (s *Transactions) attachItems(ctx context.Context, transactions []*model.Transaction) error {
	if len(transactions) == 0 {
		return nil
	}

	ids := make([]string, 0, len(transactions))
	for _, tx := range transactions {
		ids = append(ids, tx.TxID)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT tx_id, asset_id, price, result
		FROM transaction_item
		WHERE tx_id = ANY($1)
		ORDER BY tx_id, asset_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()

	byTx := make(map[string][]model.TransactionItem, len(transactions))
	for rows.Next() {
		var (
			txID string
			item model.TransactionItem
		)
		if err := rows.Scan(&txID, &item.AssetID, &item.Price, &item.Result); err != nil {
			return err
		}
		byTx[txID] = append(byTx[txID], item)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, tx := range transactions {
		tx.Items = byTx[tx.TxID]
	}
	return nil
}

func scanTransaction(row scanner) (model.Transaction, error) {
	var (
		tx         model.Transaction
		universeID *int64
		placeID    *int64
		jobID      *string
	)
	err := row.Scan(&tx.TxID, &tx.UserID, &universeID, &placeID, &jobID,
		&tx.IdempotencyKey, &tx.Status, &tx.OccurredAt, &tx.ReceivedAt)
	if err != nil {
		return model.Transaction{}, err
	}

	if universeID != nil {
		tx.UniverseID = *universeID
	}
	if placeID != nil {
		tx.PlaceID = *placeID
	}
	if jobID != nil {
		tx.JobID = *jobID
	}
	tx.Items = []model.TransactionItem{}
	return tx, nil
}

func collectTransactions(rows pgx.Rows) ([]model.Transaction, error) {
	defer rows.Close()

	var out []model.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tx)
	}
	return out, rows.Err()
}

func nullableInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
