// Package auth berisi format kunci API beserta cara memverifikasinya.
//
// Paket ini sengaja tidak tahu apa-apa soal HTTP maupun penyimpanan: ia hanya
// menerbitkan token, membacanya kembali, dan mencocokkannya dengan hash. Jalur
// HTTP ada di internal/httpapi, penyimpanannya di internal/store.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
)

// Bentuk token: acb_<env>_<keyId>_<secret>
//
//	acb      penanda milik layanan ini, memudahkan pemindai rahasia mengenalinya
//	env      "live" atau "test" — kunci produksi tidak bisa tertukar dengan lokal
//	keyId    pengenal publik; dipakai mencari barisnya tanpa memindai seluruh tabel
//	secret   256 bit acak
//
// keyId dan secret dikodekan base32 huruf kecil (a-z2-7), BUKAN base64url.
// Alfabet base64url memuat "_" dan "-", sedangkan "_" dipakai sebagai pemisah
// bagian di sini — token yang rahasianya kebetulan memuat "_" akan terbaca
// sebagai lima bagian dan ditolak walau sah. Base32 tidak punya karakter yang
// bertabrakan, dan hasilnya tetap aman ditempel di header maupun URL.
//
// keyId ada supaya verifikasi cukup satu lookup indeks. Tanpa itu, backend
// harus membaca seluruh kunci lalu membandingkan satu per satu — makin banyak
// konsumen, makin lambat, dan lama pencariannya ikut membocorkan informasi.
const (
	tokenPrefix = "acb"

	// EnvLive dan EnvTest memisahkan kunci produksi dari kunci pengembangan.
	EnvLive = "live"
	EnvTest = "test"

	keyIDBytes  = 8  // 64 bit; cukup unik dan tetap pendek dibaca manusia
	secretBytes = 32 // 256 bit
)

// ErrMalformedToken dikembalikan saat token tidak berbentuk kunci API sama
// sekali. Pemanggil HTTP wajib menjawabnya sama dengan token yang salah —
// membedakan "bentuknya salah" dari "tidak dikenal" memberi tahu penyerang
// bahwa tebakannya sudah berbentuk benar.
var ErrMalformedToken = errors.New("auth: bentuk token tidak dikenali")

// Token adalah kunci API yang baru diterbitkan.
//
// Secret hanya ada di sini, sekali, saat penerbitan. Yang tersimpan di database
// cuma Hash-nya, jadi token yang hilang tidak bisa dipulihkan — hanya bisa
// diterbitkan ulang.
type Token struct {
	KeyID string
	Hash  []byte
	// Secret adalah string utuh yang dipakai klien di header Authorization.
	Secret string
}

// Generate menerbitkan kunci API baru untuk lingkungan env.
func Generate(env string) (Token, error) {
	if env != EnvLive && env != EnvTest {
		return Token{}, fmt.Errorf("auth: env %q tidak dikenal", env)
	}

	keyID, err := randomString(keyIDBytes)
	if err != nil {
		return Token{}, err
	}
	secret, err := randomString(secretBytes)
	if err != nil {
		return Token{}, err
	}

	full := strings.Join([]string{tokenPrefix, env, keyID, secret}, "_")
	return Token{KeyID: keyID, Hash: HashToken(full), Secret: full}, nil
}

// ParseKeyID membaca keyId dari sebuah token tanpa memverifikasi apa pun.
// Dipakai untuk mencari baris kuncinya lebih dulu.
func ParseKeyID(token string) (string, error) {
	parts := strings.Split(token, "_")
	if len(parts) != 4 || parts[0] != tokenPrefix || parts[2] == "" || parts[3] == "" {
		return "", ErrMalformedToken
	}
	if parts[1] != EnvLive && parts[1] != EnvTest {
		return "", ErrMalformedToken
	}
	return parts[2], nil
}

// HashToken mengembalikan SHA-256 dari token utuh.
//
// SHA-256, bukan bcrypt/argon2, dan itu disengaja: keduanya dirancang untuk
// rahasia beruang rendah seperti kata sandi manusia, yang perlu diperlambat
// supaya tidak bisa ditebak dari kamus. Token di sini 256 bit acak — tidak ada
// kamus yang bisa menebaknya, jadi memperlambat verifikasi hanya menambah
// latensi di setiap request tanpa menambah keamanan sedikit pun.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Equal membandingkan dua hash dalam waktu tetap.
//
// Perbandingan biasa berhenti di byte pertama yang berbeda, sehingga lama
// eksekusinya ikut memberi tahu seberapa jauh tebakan penyerang sudah benar.
func Equal(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// tokenEncoding adalah base32 tanpa padding; hasilnya dihuruf-kecilkan supaya
// token enak dibaca dan disalin.
var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: gagal membangkitkan nilai acak: %w", err)
	}
	return strings.ToLower(tokenEncoding.EncodeToString(buf)), nil
}
