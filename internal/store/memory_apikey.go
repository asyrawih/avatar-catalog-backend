package store

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
)

// MemoryAPIKeys adalah implementasi in-memory dari APIKeys.
//
// Dipakai mode tanpa Postgres. Kuncinya hilang saat proses berhenti, jadi mode
// itu memang untuk pengembangan — kunci produksi hidup di database.
type MemoryAPIKeys struct {
	mu   sync.RWMutex
	rows map[string]auth.Key
}

// NewMemoryAPIKeys membuat penyimpanan kunci kosong.
func NewMemoryAPIKeys() *MemoryAPIKeys {
	return &MemoryAPIKeys{rows: make(map[string]auth.Key)}
}

var _ APIKeys = (*MemoryAPIKeys)(nil)

// ByKeyID mengembalikan kunci apa adanya, termasuk yang dicabut.
func (s *MemoryAPIKeys) ByKeyID(_ context.Context, keyID string) (auth.Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key, ok := s.rows[keyID]
	if !ok {
		return auth.Key{}, ErrNotFound
	}
	return cloneKey(key), nil
}

// Create menyimpan kunci baru.
func (s *MemoryAPIKeys) Create(_ context.Context, key auth.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rows[key.KeyID] = cloneKey(key)
	return nil
}

// List mengembalikan seluruh kunci, terbaru dulu.
func (s *MemoryAPIKeys) List(_ context.Context) ([]auth.Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]auth.Key, 0, len(s.rows))
	for _, key := range s.rows {
		out = append(out, cloneKey(key))
	}
	slices.SortFunc(out, func(a, b auth.Key) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// Revoke menandai kunci dicabut.
func (s *MemoryAPIKeys) Revoke(_ context.Context, keyID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.rows[keyID]
	if !ok {
		return ErrNotFound
	}
	if key.RevokedAt == nil {
		key.RevokedAt = &at
		s.rows[keyID] = key
	}
	return nil
}

// Update mengubah nama dan masa berlaku kunci.
func (s *MemoryAPIKeys) Update(_ context.Context, keyID string, name string, expiresAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.rows[keyID]
	if !ok {
		return ErrNotFound
	}
	key.Name = name
	key.ExpiresAt = expiresAt
	s.rows[keyID] = key
	return nil
}

// Delete menghapus baris kunci sepenuhnya.
func (s *MemoryAPIKeys) Delete(_ context.Context, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.rows[keyID]; !ok {
		return ErrNotFound
	}
	delete(s.rows, keyID)
	return nil
}

// TouchLastUsed mencatat pemakaian terakhir.
func (s *MemoryAPIKeys) TouchLastUsed(_ context.Context, keyID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.rows[keyID]
	if !ok {
		return ErrNotFound
	}
	key.LastUsedAt = &at
	s.rows[keyID] = key
	return nil
}

// cloneKey menyalin slice dan pointer supaya pemanggil tidak bisa mengubah isi
// penyimpanan lewat nilai yang dikembalikan.
func cloneKey(key auth.Key) auth.Key {
	out := key
	out.Hash = append([]byte(nil), key.Hash...)
	out.Scopes = append([]auth.Scope(nil), key.Scopes...)
	out.ExpiresAt = cloneTime(key.ExpiresAt)
	out.RevokedAt = cloneTime(key.RevokedAt)
	out.LastUsedAt = cloneTime(key.LastUsedAt)
	return out
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copied := *t
	return &copied
}
