package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/ratelimit"
)

// Limiter membatasi jumlah request. Dibuat interface supaya test bisa memasang
// pembatas palsu tanpa menunggu jendela waktu sungguhan berlalu.
type Limiter interface {
	// Allow mencatat satu request untuk key. Nilai kedua adalah sisa detik
	// sebelum jendela berikutnya dibuka.
	Allow(key string) (bool, int)
}

var _ Limiter = (*ratelimit.FixedWindow)(nil)

// rateLimit menolak request yang melewati batas, per pemanggil.
//
// Kuncinya keyId, bukan alamat IP. Semua request dari satu game server datang
// dari IP yang sama, jadi membatasi per IP berarti membatasi seluruh pemain di
// server itu sebagai satu kesatuan — dan sebaliknya, penyerang di belakang
// banyak IP tidak terbatasi sama sekali. keyId juga yang kita cabut kalau ada
// yang mengamuk, jadi masuk akal batasnya melekat di situ.
//
// Dipasang SESUDAH authenticate: pembatas butuh tahu kunci mana yang memanggil.
// Konsekuensinya request tanpa kunci yang sah tidak terbatasi di lapisan ini —
// permintaan seperti itu ditolak 401 sebelum menyentuh handler mana pun, dan
// membatasi jalur yang sudah tertutup hanya menambah biaya. Perlindungan
// terhadap banjir request tanpa kunci adalah tugas ingress/WAF di depannya.
func rateLimit(limiter Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if limiter == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, retryAfter := limiter.Allow(rateLimitKey(r))
			if !allowed {
				writeError(w, apierr.RateLimited("rate_limited",
					fmt.Sprintf("Terlalu banyak request; coba lagi dalam %d detik", retryAfter)).
					WithRetryAfter(retryAfter))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimitKey memilih pengenal yang dibatasi.
//
// keyId kalau pemanggilnya membawa kunci. Mode tanpa kunci (pengembangan
// lokal) jatuh ke alamat IP, supaya pembatas tetap bisa diuji di sana dan
// tidak diam-diam menganggap semua pemanggil satu orang yang sama.
func rateLimitKey(r *http.Request) string {
	if keyID := callerFrom(r.Context()).KeyID; keyID != "" {
		return "key:" + keyID
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

// NewRateLimiter merangkai pembatas dari konfigurasi. perSecond <= 0 berarti
// pembatasan mati.
func NewRateLimiter(perSecond int) Limiter {
	if perSecond <= 0 {
		return nil
	}
	return ratelimit.NewFixedWindow(perSecond, rateLimitWindow)
}

// rateLimitWindow adalah panjang jendelanya. Satu detik: batasnya dinyatakan
// per detik, dan jendela sependek ini membuat efek "jatah habis di awal
// jendela lalu diam sampai jendela berikutnya" — kelemahan bawaan pembatas
// jendela-tetap — paling lama hanya berlangsung satu detik.
const rateLimitWindow = time.Second
