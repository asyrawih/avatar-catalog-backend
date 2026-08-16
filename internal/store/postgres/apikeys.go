package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// APIKeys adalah implementasi store.APIKeys di atas Postgres.
type APIKeys struct {
	pool *pgxpool.Pool
}

// NewAPIKeys merangkai penyimpanan kunci API.
func NewAPIKeys(pool *pgxpool.Pool) *APIKeys { return &APIKeys{pool: pool} }

var _ store.APIKeys = (*APIKeys)(nil)

const apiKeyColumns = `key_id, token_hash, name, scopes, created_at, expires_at, revoked_at, last_used_at`

// ByKeyID mengembalikan kunci apa adanya, termasuk yang dicabut atau
// kedaluwarsa — pemanggil yang memutuskan.
func (s *APIKeys) ByKeyID(ctx context.Context, keyID string) (auth.Key, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_key WHERE key_id = $1`, keyID)

	key, err := scanAPIKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Key{}, store.ErrNotFound
	}
	return key, err
}

// Create menyimpan kunci baru.
func (s *APIKeys) Create(ctx context.Context, key auth.Key) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO api_key (key_id, token_hash, name, scopes, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		key.KeyID, key.Hash, key.Name, auth.ScopeStrings(key.Scopes), key.CreatedAt, key.ExpiresAt)
	return err
}

// List mengembalikan seluruh kunci, terbaru dulu.
func (s *APIKeys) List(ctx context.Context) ([]auth.Key, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+apiKeyColumns+` FROM api_key ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []auth.Key
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

// Revoke menandai kunci dicabut. Idempoten: revoked_at yang sudah terisi tidak
// ditimpa, supaya waktu pencabutan pertama tetap tercatat apa adanya.
func (s *APIKeys) Revoke(ctx context.Context, keyID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_key SET revoked_at = coalesce(revoked_at, $2) WHERE key_id = $1`, keyID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// Update mengubah nama dan masa berlaku kunci.
func (s *APIKeys) Update(ctx context.Context, keyID string, name string, expiresAt *time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_key SET name = $2, expires_at = $3 WHERE key_id = $1`,
		keyID, name, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// Delete menghapus baris kunci sepenuhnya.
func (s *APIKeys) Delete(ctx context.Context, keyID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM api_key WHERE key_id = $1`, keyID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// TouchLastUsed mencatat pemakaian terakhir, paling sering sekali per menit per
// kunci.
//
// Tanpa ambang itu, setiap panggilan API akan menulis ke baris yang sama; satu
// game server yang sibuk berarti satu UPDATE per request, semuanya berebut baris
// yang sama, dan tabel jadi penuh baris mati. Ketelitian sampai menit sudah
// cukup untuk pertanyaan yang mau dijawab kolom ini: "kunci ini masih dipakai
// atau sudah bisa dicabut?"
func (s *APIKeys) TouchLastUsed(ctx context.Context, keyID string, at time.Time) error {
	// $2 wajib di-cast eksplisit. Tanpa itu Postgres menyimpulkan tipe parameter
	// dari "$2 - interval '1 minute'" sebagai interval, lalu menolak
	// perbandingan "timestamptz < interval" — dan karena kegagalan di sini cuma
	// di-log, kolomnya diam-diam tidak pernah terisi.
	_, err := s.pool.Exec(ctx, `
		UPDATE api_key SET last_used_at = $2::timestamptz
		WHERE key_id = $1
		  AND (last_used_at IS NULL OR last_used_at < $2::timestamptz - interval '1 minute')`, keyID, at)
	return err
}

func scanAPIKey(row scanner) (auth.Key, error) {
	var (
		key    auth.Key
		scopes []string
	)
	err := row.Scan(&key.KeyID, &key.Hash, &key.Name, &scopes,
		&key.CreatedAt, &key.ExpiresAt, &key.RevokedAt, &key.LastUsedAt)
	if err != nil {
		return auth.Key{}, err
	}
	key.Scopes = auth.ScopesFromStrings(scopes)
	return key, nil
}
