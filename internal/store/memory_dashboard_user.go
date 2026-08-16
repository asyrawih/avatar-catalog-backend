package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
)

// MemoryDashboardUsers adalah implementasi in-memory dari DashboardUsers.
//
// Dipakai mode tanpa Postgres. Operator dan sesinya hilang saat proses
// berhenti — mode itu memang untuk pengembangan.
type MemoryDashboardUsers struct {
	mu       sync.RWMutex
	users    map[string]auth.User
	sessions map[string]auth.Session
}

// NewMemoryDashboardUsers membuat penyimpanan operator kosong.
func NewMemoryDashboardUsers() *MemoryDashboardUsers {
	return &MemoryDashboardUsers{
		users:    make(map[string]auth.User),
		sessions: make(map[string]auth.Session),
	}
}

var _ DashboardUsers = (*MemoryDashboardUsers)(nil)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *MemoryDashboardUsers) ByEmail(_ context.Context, email string) (auth.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	want := normalizeEmail(email)
	for _, user := range s.users {
		if user.Email == want {
			return user, nil
		}
	}
	return auth.User{}, ErrNotFound
}

func (s *MemoryDashboardUsers) ByID(_ context.Context, userID string) (auth.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[userID]
	if !ok {
		return auth.User{}, ErrNotFound
	}
	return user, nil
}

func (s *MemoryDashboardUsers) Create(_ context.Context, user auth.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user.Email = normalizeEmail(user.Email)
	for _, existing := range s.users {
		if existing.Email == user.Email {
			return ErrConflict
		}
	}
	s.users[user.UserID] = user
	return nil
}

func (s *MemoryDashboardUsers) List(_ context.Context) ([]auth.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]auth.User, 0, len(s.users))
	for _, user := range s.users {
		out = append(out, user)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryDashboardUsers) SetDisabled(_ context.Context, userID string, at *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return ErrNotFound
	}
	user.DisabledAt = at
	s.users[userID] = user
	return nil
}

func (s *MemoryDashboardUsers) SetPassword(_ context.Context, userID, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return ErrNotFound
	}
	user.PasswordHash = passwordHash
	s.users[userID] = user
	return nil
}

func (s *MemoryDashboardUsers) UpdateProfile(_ context.Context, userID, email, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return ErrNotFound
	}
	next := normalizeEmail(email)
	for id, existing := range s.users {
		if id != userID && existing.Email == next {
			return ErrConflict
		}
	}
	user.Email, user.Name = next, name
	s.users[userID] = user
	return nil
}

// Delete menghapus operator beserta seluruh sesinya.
func (s *MemoryDashboardUsers) Delete(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[userID]; !ok {
		return ErrNotFound
	}
	delete(s.users, userID)
	for id, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, id)
		}
	}
	return nil
}

func (s *MemoryDashboardUsers) TouchLastLogin(_ context.Context, userID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return ErrNotFound
	}
	user.LastLoginAt = &at
	s.users[userID] = user
	return nil
}

func (s *MemoryDashboardUsers) CreateSession(_ context.Context, session auth.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[session.SessionID] = session
	return nil
}

func (s *MemoryDashboardUsers) SessionByID(_ context.Context, sessionID string) (auth.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return auth.Session{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryDashboardUsers) RevokeSession(_ context.Context, sessionID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	// Waktu pencabutan pertama yang dipertahankan; mencabut dua kali bukan
	// error.
	if session.RevokedAt == nil {
		session.RevokedAt = &at
		s.sessions[sessionID] = session
	}
	return nil
}

func (s *MemoryDashboardUsers) RevokeUserSessions(_ context.Context, userID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, session := range s.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &at
			s.sessions[id] = session
		}
	}
	return nil
}

func (s *MemoryDashboardUsers) TouchSession(_ context.Context, sessionID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	session.LastSeenAt = &at
	s.sessions[sessionID] = session
	return nil
}
