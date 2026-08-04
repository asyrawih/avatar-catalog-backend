package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis adalah Cache di atas Redis.
type Redis struct {
	client *redis.Client
	prefix string
}

var _ Cache = (*Redis)(nil)

// OpenRedis membuka koneksi dan memastikan server benar-benar menjawab
// sebelum server HTTP mulai melayani.
//
// prefix memisahkan keyspace antar environment yang berbagi satu instance.
func OpenRedis(ctx context.Context, url, prefix string) (*Redis, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("cache: REDIS_URL tidak valid: %w", err)
	}

	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("cache: gagal terhubung ke redis: %w", err)
	}
	return &Redis{client: client, prefix: prefix}, nil
}

// Get membaca nilai ber-JSON ke dst.
func (r *Redis) Get(ctx context.Context, key string, dst any) (bool, error) {
	raw, err := r.client.Get(ctx, r.key(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		// Entri rusak atau bentuknya berubah setelah deploy; buang lalu
		// perlakukan sebagai miss.
		_ = r.client.Del(ctx, r.key(key)).Err()
		return false, err
	}
	return true, nil
}

// Set menyimpan nilai sebagai JSON dengan umur ttl.
func (r *Redis) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, r.key(key), raw, ttl).Err()
}

// Delete membuang kunci tertentu.
func (r *Redis) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	prefixed := make([]string, 0, len(keys))
	for _, key := range keys {
		prefixed = append(prefixed, r.key(key))
	}
	return r.client.Del(ctx, prefixed...).Err()
}

// Version membaca nomor versi sebuah namespace.
func (r *Redis) Version(ctx context.Context, namespace string) (int64, error) {
	raw, err := r.client.Get(ctx, r.versionKey(namespace)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

// BumpVersion menaikkan versi namespace.
func (r *Redis) BumpVersion(ctx context.Context, namespace string) error {
	return r.client.Incr(ctx, r.versionKey(namespace)).Err()
}

// Ping memeriksa koneksi.
func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

// Close menutup koneksi.
func (r *Redis) Close() error { return r.client.Close() }

// Client membuka akses ke klien mentah untuk keperluan lain, mis. penyimpan
// kunci idempotensi.
func (r *Redis) Client() *redis.Client { return r.client }

// Prefix mengembalikan awalan keyspace yang dipakai instance ini.
func (r *Redis) Prefix() string { return r.prefix }

func (r *Redis) key(key string) string {
	if r.prefix == "" {
		return key
	}
	return r.prefix + ":" + key
}

func (r *Redis) versionKey(namespace string) string { return r.key("ver:" + namespace) }
