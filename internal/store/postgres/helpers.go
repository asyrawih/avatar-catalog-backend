package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hanan/avatar-catalog-backend/internal/paging"
)

// uniqueViolation adalah SQLSTATE untuk pelanggaran batasan unik.
const uniqueViolation = "23505"

// isUniqueViolation melaporkan apakah err berasal dari batasan unik bernama
// constraint.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == uniqueViolation && pgErr.ConstraintName == constraint
}

// inTx menjalankan fn dalam satu transaksi, rollback bila fn gagal.
func (s *Outfits) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return runInTx(ctx, s.pool.Begin, fn)
}

func (s *Transactions) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return runInTx(ctx, s.pool.Begin, fn)
}

func runInTx(ctx context.Context, begin func(context.Context) (pgx.Tx, error), fn func(pgx.Tx) error) error {
	tx, err := begin(ctx)
	if err != nil {
		return err
	}
	// Rollback setelah commit sukses tidak berbahaya: pgx mengembalikan
	// ErrTxClosed yang sengaja diabaikan di sini.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ensurePlayer memastikan baris PLAYER ada sebelum outfit atau transaksi
// menunjuknya lewat foreign key.
//
// Backend tidak punya sumber kebenaran untuk username — nilai itu datang
// belakangan dari game server — jadi baris minimal ini cukup untuk memenuhi
// FK tanpa mengarang data.
func ensurePlayer(ctx context.Context, tx pgx.Tx, userID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO player (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING`, userID)
	return err
}

// tagsOrEmpty menjaga kolom jsonb selalu berisi array, bukan null.
func tagsOrEmpty(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// nullableTime mengubah cursor kosong menjadi NULL supaya satu query bisa
// melayani halaman pertama maupun lanjutan.
func nullableTime(cursor *paging.KeysetCursor, at time.Time) *time.Time {
	if cursor == nil {
		return nil
	}
	return &at
}

// likeEscaper menetralkan wildcard ILIKE supaya keyword dicari apa adanya —
// nama outfit boleh memuat "%" atau "_" tanpa berubah makna jadi pola.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likePattern membungkus keyword menjadi pola "mengandung", atau nil bila
// keyword kosong sehingga syarat pencarian di query ikut mati.
func likePattern(keyword string) *string {
	if keyword == "" {
		return nil
	}
	pattern := "%" + likeEscaper.Replace(keyword) + "%"
	return &pattern
}

// trimPage memotong hasil yang sengaja diambil satu lebih banyak dari limit,
// lalu melaporkan apakah masih ada halaman berikutnya.
func trimPage[T any](rows []T, limit int) ([]T, bool) {
	if rows == nil {
		rows = []T{}
	}
	if limit > 0 && len(rows) > limit {
		return rows[:limit], true
	}
	return rows, false
}
