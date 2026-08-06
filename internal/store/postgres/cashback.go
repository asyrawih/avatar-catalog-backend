package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Cashback adalah implementasi store.Cashback di atas Postgres.
type Cashback struct {
	pool *pgxpool.Pool
}

// NewCashback merangkai penyimpanan cashback.
func NewCashback(pool *pgxpool.Pool) *Cashback { return &Cashback{pool: pool} }

var _ store.Cashback = (*Cashback)(nil)

func (s *Cashback) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return runInTx(ctx, s.pool.Begin, fn)
}

const entryColumns = `
	entry_id, user_id, tx_id, request_id, kind, spend, rate_percent, amount, created_at`

// SpendStats membaca riwayat belanja sukses pemain langsung dari TRANSACTION +
// TRANSACTION_ITEM — sumber kebenaran spend, bukan ledger.
func (s *Cashback) SpendStats(ctx context.Context, userID int64, excludeTxID string, daysSince time.Time) (store.SpendStats, error) {
	stats := store.SpendStats{SpendDays: make(map[string]bool)}

	// Bagian paket membawa harga bundle induk yang terulang per bagian, jadi
	// spend menghitung tiap (tx, bundle) sekali — baris pertama tiap kelompok
	// yang dihitung; item satuan (bundle_id NULL) dihitung semua.
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(price), 0), COUNT(DISTINCT tx_id)
		FROM (
			SELECT t.tx_id, ti.price,
			       ti.bundle_id IS NULL
			           OR row_number() OVER (PARTITION BY t.tx_id, ti.bundle_id
			                                 ORDER BY ti.asset_id) = 1 AS counted
			FROM transaction t
			JOIN transaction_item ti ON ti.tx_id = t.tx_id
			WHERE t.user_id = $1
			  AND ti.result = 'success'
			  AND ti.price > 0
			  AND t.tx_id <> $2
		) spend
		WHERE counted`,
		userID, excludeTxID).Scan(&stats.LifetimeSpend, &stats.PurchaseCount)
	if err != nil {
		return store.SpendStats{}, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT to_char(t.occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')
		FROM transaction t
		JOIN transaction_item ti ON ti.tx_id = t.tx_id
		WHERE t.user_id = $1
		  AND ti.result = 'success'
		  AND ti.price > 0
		  AND t.tx_id <> $2
		  AND t.occurred_at >= $3`,
		userID, excludeTxID, daysSince)
	if err != nil {
		return store.SpendStats{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return store.SpendStats{}, err
		}
		stats.SpendDays[day] = true
	}
	return stats, rows.Err()
}

// CreateEntry menambah satu baris ledger dengan dedup (txId, kind).
func (s *Cashback) CreateEntry(ctx context.Context, e model.CashbackEntry) error {
	err := s.inTx(ctx, func(dbTx pgx.Tx) error {
		if err := ensurePlayer(ctx, dbTx, e.UserID); err != nil {
			return err
		}
		return insertEntry(ctx, dbTx, e)
	})
	if isUniqueViolation(err, "cashback_entry_tx_kind_key") {
		return store.ErrDuplicateEntry
	}
	return err
}

func insertEntry(ctx context.Context, dbTx pgx.Tx, e model.CashbackEntry) error {
	_, err := dbTx.Exec(ctx, `
		INSERT INTO cashback_entry (entry_id, user_id, tx_id, request_id, kind,
		                            spend, rate_percent, amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.EntryID, e.UserID, nullableString(e.TxID), nullableString(e.RequestID),
		e.Kind, e.Spend, e.RatePercent, e.Amount, e.CreatedAt)
	return err
}

// EntryByTxKind mencari baris ledger sebuah transaksi untuk kind tertentu.
func (s *Cashback) EntryByTxKind(ctx context.Context, txID string, kind model.CashbackKind) (model.CashbackEntry, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT`+entryColumns+`
		FROM cashback_entry
		WHERE tx_id = $1 AND kind = $2`, txID, kind)

	e, err := scanEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.CashbackEntry{}, false, nil
	}
	if err != nil {
		return model.CashbackEntry{}, false, err
	}
	return e, true, nil
}

// Balance menjumlahkan seluruh amount pemain.
func (s *Cashback) Balance(ctx context.Context, userID int64) (int, error) {
	var total int
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM cashback_entry
		WHERE user_id = $1`, userID).Scan(&total)
	return total, err
}

// ListEntries mengembalikan satu halaman ledger pemain, terbaru dulu.
func (s *Cashback) ListEntries(ctx context.Context, userID int64, after *paging.KeysetCursor, limit int) ([]model.CashbackEntry, bool, error) {
	var (
		cursorAt time.Time
		cursorID string
	)
	if after != nil {
		cursorAt, cursorID = after.At, after.ID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT`+entryColumns+`
		FROM cashback_entry
		WHERE user_id = $1
		  AND ($2::timestamptz IS NULL
		       OR created_at < $2
		       OR (created_at = $2 AND entry_id > $3))
		ORDER BY created_at DESC, entry_id ASC
		LIMIT $4`,
		userID, nullableTime(after, cursorAt), cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var entries []model.CashbackEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, false, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	entries, hasMore := trimPage(entries, limit)
	return entries, hasMore, nil
}

// CreateRedeem menyimpan request pending plus baris pemotong saldo dalam satu
// transaksi database. Batasan unik parsial redeem_request_pending_key yang
// menegakkan "maksimal satu pending per pemain" — bukan pemeriksaan baca-tulis.
func (s *Cashback) CreateRedeem(ctx context.Context, r model.RedeemRequest, deduct model.CashbackEntry) error {
	err := s.inTx(ctx, func(dbTx pgx.Tx) error {
		_, err := dbTx.Exec(ctx, `
			INSERT INTO redeem_request (request_id, user_id, amount, status, requested_at, resolved_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			r.RequestID, r.UserID, r.Amount, r.Status, r.RequestedAt, r.ResolvedAt)
		if err != nil {
			return err
		}
		return insertEntry(ctx, dbTx, deduct)
	})
	if isUniqueViolation(err, "redeem_request_pending_key") {
		return store.ErrPendingRedeemExists
	}
	return err
}

const redeemColumns = `request_id, user_id, amount, status, requested_at, resolved_at`

// GetRedeem mengembalikan satu request redeem.
func (s *Cashback) GetRedeem(ctx context.Context, requestID string) (model.RedeemRequest, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+redeemColumns+` FROM redeem_request WHERE request_id = $1`, requestID)
	r, err := scanRedeem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.RedeemRequest{}, store.ErrNotFound
	}
	return r, err
}

// ResolveRedeem menerapkan fn pada request tersimpan lalu menyimpan hasilnya.
// Baris dikunci FOR UPDATE supaya dua resolusi bersamaan tidak saling menimpa.
func (s *Cashback) ResolveRedeem(ctx context.Context, requestID string, fn func(*model.RedeemRequest) (*model.CashbackEntry, error)) (model.RedeemRequest, error) {
	var out model.RedeemRequest
	err := s.inTx(ctx, func(dbTx pgx.Tx) error {
		row := dbTx.QueryRow(ctx, `
			SELECT `+redeemColumns+`
			FROM redeem_request WHERE request_id = $1
			FOR UPDATE`, requestID)
		r, err := scanRedeem(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}

		entry, err := fn(&r)
		if err != nil {
			return err
		}

		_, err = dbTx.Exec(ctx, `
			UPDATE redeem_request SET status = $2, resolved_at = $3
			WHERE request_id = $1`,
			requestID, r.Status, r.ResolvedAt)
		if err != nil {
			return err
		}
		if entry != nil {
			if err := insertEntry(ctx, dbTx, *entry); err != nil {
				return err
			}
		}
		out = r
		return nil
	})
	return out, err
}

// ListRedeems mengembalikan satu halaman request yang cocok dengan filter.
func (s *Cashback) ListRedeems(ctx context.Context, f store.RedeemFilter, after *paging.KeysetCursor, limit int) ([]model.RedeemRequest, bool, error) {
	var (
		cursorAt time.Time
		cursorID string
	)
	if after != nil {
		cursorAt, cursorID = after.At, after.ID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+redeemColumns+`
		FROM redeem_request
		WHERE ($1::bigint = 0 OR user_id = $1)
		  AND ($2 = '' OR status = $2)
		  AND ($3::timestamptz IS NULL
		       OR requested_at < $3
		       OR (requested_at = $3 AND request_id > $4))
		ORDER BY requested_at DESC, request_id ASC
		LIMIT $5`,
		f.UserID, f.Status, nullableTime(after, cursorAt), cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var redeems []model.RedeemRequest
	for rows.Next() {
		r, err := scanRedeem(rows)
		if err != nil {
			return nil, false, err
		}
		redeems = append(redeems, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	redeems, hasMore := trimPage(redeems, limit)
	return redeems, hasMore, nil
}

// CreateEvent menjadwalkan satu jendela bonus event.
func (s *Cashback) CreateEvent(ctx context.Context, ev model.CashbackEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO cashback_event (event_id, name, starts_at, ends_at)
		VALUES ($1, $2, $3, $4)`,
		ev.EventID, ev.Name, ev.StartsAt, ev.EndsAt)
	return err
}

// ActiveEvent mencari event yang jendelanya mencakup at.
func (s *Cashback) ActiveEvent(ctx context.Context, at time.Time) (model.CashbackEvent, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT event_id, name, starts_at, ends_at
		FROM cashback_event
		WHERE starts_at <= $1 AND ends_at > $1
		ORDER BY starts_at
		LIMIT 1`, at)

	var ev model.CashbackEvent
	err := row.Scan(&ev.EventID, &ev.Name, &ev.StartsAt, &ev.EndsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.CashbackEvent{}, false, nil
	}
	if err != nil {
		return model.CashbackEvent{}, false, err
	}
	return ev, true, nil
}

// ListEvents mengembalikan event yang belum berakhir pada from, urut mulai.
func (s *Cashback) ListEvents(ctx context.Context, from time.Time) ([]model.CashbackEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event_id, name, starts_at, ends_at
		FROM cashback_event
		WHERE ends_at > $1
		ORDER BY starts_at, event_id`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []model.CashbackEvent{}
	for rows.Next() {
		var ev model.CashbackEvent
		if err := rows.Scan(&ev.EventID, &ev.Name, &ev.StartsAt, &ev.EndsAt); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// Totals mengagregat ledger pada rentang [from, to).
func (s *Cashback) Totals(ctx context.Context, from, to time.Time) (model.CashbackTotals, error) {
	var totals model.CashbackTotals
	err := s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(amount) FILTER (WHERE kind = 'accrual'
				AND created_at >= $1 AND created_at < $2), 0),
			COALESCE(-SUM(amount) FILTER (WHERE kind = 'reversal'
				AND created_at >= $1 AND created_at < $2), 0),
			COALESCE(-SUM(amount) FILTER (WHERE kind = 'redeem'
				AND created_at >= $1 AND created_at < $2), 0),
			COALESCE(SUM(amount) FILTER (WHERE kind = 'redeem_return'
				AND created_at >= $1 AND created_at < $2), 0),
			COALESCE(SUM(amount), 0)
		FROM cashback_entry`, from, to).
		Scan(&totals.Accrued, &totals.Reversed, &totals.Redeemed, &totals.Returned, &totals.Outstanding)
	return totals, err
}

func scanEntry(row scanner) (model.CashbackEntry, error) {
	var (
		e         model.CashbackEntry
		txID      *string
		requestID *string
	)
	err := row.Scan(&e.EntryID, &e.UserID, &txID, &requestID, &e.Kind,
		&e.Spend, &e.RatePercent, &e.Amount, &e.CreatedAt)
	if err != nil {
		return model.CashbackEntry{}, err
	}
	if txID != nil {
		e.TxID = *txID
	}
	if requestID != nil {
		e.RequestID = *requestID
	}
	return e, nil
}

func scanRedeem(row scanner) (model.RedeemRequest, error) {
	var r model.RedeemRequest
	err := row.Scan(&r.RequestID, &r.UserID, &r.Amount, &r.Status, &r.RequestedAt, &r.ResolvedAt)
	if err != nil {
		return model.RedeemRequest{}, err
	}
	return r, nil
}
