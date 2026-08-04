// Package idempotency menyimpan hasil POST yang sudah pernah diproses supaya
// retry dari sisi Roblox (timeout padahal backend sudah menulis) tidak membuat
// data ganda.
package idempotency

import (
	"sync"
	"time"
)

// DefaultTTL adalah umur simpan sebuah kunci.
const DefaultTTL = 24 * time.Hour

// Record adalah respons yang disimpan untuk sebuah kunci.
type Record struct {
	Status    int
	Body      []byte
	CreatedAt time.Time
}

// Store adalah port penyimpanan kunci idempotensi.
type Store interface {
	Get(scope, key string) (Record, bool)
	Put(scope, key string, rec Record)
}

// MemoryStore adalah Store in-memory dengan kedaluwarsa berbasis TTL.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Record
	ttl     time.Duration
	now     func() time.Time
}

// NewMemoryStore membuat store kosong. TTL <= 0 memakai DefaultTTL.
func NewMemoryStore(ttl time.Duration) *MemoryStore {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &MemoryStore{
		records: make(map[string]Record),
		ttl:     ttl,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

var _ Store = (*MemoryStore)(nil)

// Get mengembalikan rekaman yang belum kedaluwarsa untuk scope+key.
func (s *MemoryStore) Get(scope, key string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[scope+"\x00"+key]
	if !ok {
		return Record{}, false
	}
	if s.now().Sub(rec.CreatedAt) > s.ttl {
		delete(s.records, scope+"\x00"+key)
		return Record{}, false
	}
	return rec, true
}

// Put menyimpan rekaman untuk scope+key.
func (s *MemoryStore) Put(scope, key string, rec Record) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = s.now()
	}
	s.records[scope+"\x00"+key] = rec
}
