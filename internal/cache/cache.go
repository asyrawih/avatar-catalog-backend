// Package cache menyediakan cache key-value bersama untuk hasil baca API.
//
// Aturan mainnya: cache tidak boleh menjadi titik gagal. Semua kesalahan cache
// dianggap sebagai miss oleh pemanggil, sehingga permintaan tetap dilayani
// langsung dari database.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Cache adalah port penyimpanan sementara.
type Cache interface {
	// Get membaca nilai ber-JSON ke dst. Nilai kedua false berarti miss.
	Get(ctx context.Context, key string, dst any) (bool, error)
	// Set menyimpan nilai sebagai JSON dengan umur ttl.
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	// Delete membuang kunci tertentu.
	Delete(ctx context.Context, keys ...string) error
	// Version membaca nomor versi sebuah namespace; 0 berarti belum pernah diisi.
	Version(ctx context.Context, namespace string) (int64, error)
	// BumpVersion menaikkan versi namespace sehingga seluruh kunci lama
	// tidak akan pernah terbaca lagi dan hilang sendiri saat TTL habis.
	//
	// Cara ini dipilih daripada menghapus per pola: menghapus pola berarti
	// memindai seluruh keyspace Redis, yang justru berat saat katalog sering
	// berubah.
	BumpVersion(ctx context.Context, namespace string) error
	// Ping memeriksa koneksi, dipakai probe kesiapan.
	Ping(ctx context.Context) error
	// Close menutup koneksi.
	Close() error
}

// Noop adalah Cache yang selalu miss. Dipakai saat REDIS_URL tidak diisi,
// sehingga jalur kode pemanggil tetap sama tanpa percabangan.
type Noop struct{}

var _ Cache = Noop{}

func (Noop) Get(context.Context, string, any) (bool, error)        { return false, nil }
func (Noop) Set(context.Context, string, any, time.Duration) error { return nil }
func (Noop) Delete(context.Context, ...string) error               { return nil }
func (Noop) Version(context.Context, string) (int64, error)        { return 0, nil }
func (Noop) BumpVersion(context.Context, string) error             { return nil }
func (Noop) Ping(context.Context) error                            { return nil }
func (Noop) Close() error                                          { return nil }

// Key merangkai bagian-bagian kunci dengan pemisah baku.
func Key(parts ...string) string { return strings.Join(parts, ":") }

// HashInt64s memadatkan daftar id menjadi potongan hash pendek, supaya kunci
// untuk permintaan borongan tidak ikut memanjang bersama jumlah id.
//
// Urutan id ikut menentukan hash; pemanggil yang ingin id A,B dan B,A berbagi
// entri cache harus mengurutkannya lebih dulu.
func HashInt64s(ids []int64) string {
	var sb strings.Builder
	for _, id := range ids {
		sb.WriteString(strconv.FormatInt(id, 10))
		sb.WriteByte(',')
	}
	return hashString(sb.String())
}

// HashString memadatkan string bebas (mis. cursor) menjadi potongan hash.
func HashString(s string) string {
	if s == "" {
		return "first"
	}
	return hashString(s)
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
