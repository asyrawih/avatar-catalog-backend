package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// DashboardUsers adalah implementasi store.DashboardUsers di atas Postgres.
type DashboardUsers struct {
	pool *pgxpool.Pool
}

// NewDashboardUsers merangkai penyimpanan operator dashboard.
func NewDashboardUsers(pool *pgxpool.Pool) *DashboardUsers { return &DashboardUsers{pool: pool} }

var _ store.DashboardUsers = (*DashboardUsers)(nil)

const userColumns = `user_id, email, password_hash, name, created_at, disabled_at, last_login_at`

func (s *DashboardUsers) ByEmail(ctx context.Context, email string) (auth.User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM dashboard_user WHERE email = $1`,
		strings.ToLower(strings.TrimSpace(email)))

	user, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, store.ErrNotFound
	}
	return user, err
}

func (s *DashboardUsers) ByID(ctx context.Context, userID string) (auth.User, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM dashboard_user WHERE user_id = $1`, userID)

	user, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, store.ErrNotFound
	}
	return user, err
}

func (s *DashboardUsers) Create(ctx context.Context, user auth.User) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO dashboard_user (user_id, email, password_hash, name, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		user.UserID, strings.ToLower(strings.TrimSpace(user.Email)),
		user.PasswordHash, user.Name, user.CreatedAt)
	if isUniqueViolation(err, "dashboard_user_email_key") {
		return store.ErrConflict
	}
	return err
}

func (s *DashboardUsers) List(ctx context.Context) ([]auth.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM dashboard_user ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []auth.User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	return out, rows.Err()
}

func (s *DashboardUsers) SetDisabled(ctx context.Context, userID string, at *time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE dashboard_user SET disabled_at = $2 WHERE user_id = $1`, userID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *DashboardUsers) SetPassword(ctx context.Context, userID, passwordHash string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE dashboard_user SET password_hash = $2 WHERE user_id = $1`, userID, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *DashboardUsers) UpdateProfile(ctx context.Context, userID, email, name string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE dashboard_user SET email = $2, name = $3 WHERE user_id = $1`,
		userID, strings.ToLower(strings.TrimSpace(email)), name)
	if isUniqueViolation(err, "dashboard_user_email_key") {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// Delete menghapus operator; sesinya ikut lewat ON DELETE CASCADE.
func (s *DashboardUsers) Delete(ctx context.Context, userID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM dashboard_user WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *DashboardUsers) TouchLastLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE dashboard_user SET last_login_at = $2 WHERE user_id = $1`, userID, at)
	return err
}

const sessionColumns = `session_id, token_hash, user_id, created_at, expires_at, revoked_at, last_seen_at, user_agent`

func (s *DashboardUsers) CreateSession(ctx context.Context, session auth.Session) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO dashboard_session (session_id, token_hash, user_id, created_at, expires_at, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		session.SessionID, session.Hash, session.UserID,
		session.CreatedAt, session.ExpiresAt, session.UserAgent)
	return err
}

func (s *DashboardUsers) SessionByID(ctx context.Context, sessionID string) (auth.Session, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM dashboard_session WHERE session_id = $1`, sessionID)

	var session auth.Session
	err := row.Scan(&session.SessionID, &session.Hash, &session.UserID, &session.CreatedAt,
		&session.ExpiresAt, &session.RevokedAt, &session.LastSeenAt, &session.UserAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Session{}, store.ErrNotFound
	}
	return session, err
}

// RevokeSession idempoten: mencabut sesi yang sudah dicabut bukan error, dan
// waktu pencabutan pertama yang dipertahankan.
func (s *DashboardUsers) RevokeSession(ctx context.Context, sessionID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE dashboard_session SET revoked_at = COALESCE(revoked_at, $2) WHERE session_id = $1`,
		sessionID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *DashboardUsers) RevokeUserSessions(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE dashboard_session SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`,
		userID, at)
	return err
}

func (s *DashboardUsers) TouchSession(ctx context.Context, sessionID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE dashboard_session SET last_seen_at = $2 WHERE session_id = $1`, sessionID, at)
	return err
}

func scanUser(row pgx.Row) (auth.User, error) {
	var user auth.User
	err := row.Scan(&user.UserID, &user.Email, &user.PasswordHash, &user.Name,
		&user.CreatedAt, &user.DisabledAt, &user.LastLoginAt)
	return user, err
}
