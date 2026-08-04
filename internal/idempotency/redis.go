package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore menyimpan hasil POST di Redis.
//
// Redis dipilih karena kunci idempotensi memang berumur pendek dan
// kedaluwarsanya ditangani server, tanpa perlu job pembersih seperti kalau
// disimpan di tabel database.
type RedisStore struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
	logger *slog.Logger
}

var _ Store = (*RedisStore)(nil)

// NewRedisStore merangkai penyimpanan idempotensi di atas klien Redis.
func NewRedisStore(client *redis.Client, prefix string, ttl time.Duration, logger *slog.Logger) *RedisStore {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisStore{client: client, prefix: prefix, ttl: ttl, logger: logger}
}

// Get mengembalikan rekaman untuk scope+key.
//
// Kegagalan Redis dilaporkan sebagai "belum ada". Akibat terburuknya adalah
// satu permintaan diproses ulang — untuk POST outfit dan sync run itu jauh
// lebih ringan daripada menolak permintaan yang sebenarnya sah.
func (s *RedisStore) Get(scope, key string) (Record, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	raw, err := s.client.Get(ctx, s.key(scope, key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Record{}, false
	}
	if err != nil {
		s.logger.Warn("gagal membaca kunci idempotensi", "scope", scope, "err", err)
		return Record{}, false
	}

	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		s.logger.Warn("kunci idempotensi rusak, diabaikan", "scope", scope, "err", err)
		return Record{}, false
	}
	return rec, true
}

// Put menyimpan rekaman untuk scope+key.
func (s *RedisStore) Put(scope, key string, rec Record) {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}

	raw, err := json.Marshal(rec)
	if err != nil {
		s.logger.Error("gagal mengemas kunci idempotensi", "scope", scope, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.client.Set(ctx, s.key(scope, key), raw, s.ttl).Err(); err != nil {
		s.logger.Warn("gagal menyimpan kunci idempotensi", "scope", scope, "err", err)
	}
}

func (s *RedisStore) key(scope, key string) string {
	if s.prefix == "" {
		return "idem:" + scope + ":" + key
	}
	return s.prefix + ":idem:" + scope + ":" + key
}
