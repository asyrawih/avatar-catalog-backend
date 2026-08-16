package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Kata sandi operator dashboard dihash dengan argon2id, bukan SHA-256 seperti
// token kunci API.
//
// Bedanya bukan selera: token kunci API 256 bit acak, tidak ada kamus yang bisa
// menebaknya, jadi memperlambat verifikasi hanya menambah latensi. Kata sandi
// manusia sebaliknya — berentropi rendah dan bisa ditebak dari kamus — sehingga
// yang melindunginya justru biaya verifikasi yang mahal, dalam waktu maupun
// memori.
//
// Parameternya mengikuti anjuran OWASP untuk argon2id: memori 19 MiB, dua
// iterasi, paralelisme satu. Nilainya ikut tersimpan di dalam string hash, jadi
// menaikkannya nanti tidak membuat hash lama gagal diverifikasi.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrMalformedHash dikembalikan saat string hash tidak bisa dibaca. Ini
// kerusakan data, bukan kata sandi yang salah — pemanggil tidak boleh
// memperlakukannya sebagai login gagal biasa.
var ErrMalformedHash = errors.New("auth: bentuk hash kata sandi tidak dikenali")

// MinPasswordLen adalah panjang minimum kata sandi.
//
// Panjang, bukan aturan "harus ada angka dan simbol": aturan komposisi
// mendorong orang membuat variasi yang bisa ditebak dari kata yang sama,
// sedangkan panjang menambah entropi sungguhan.
const MinPasswordLen = 12

// HashPassword mengembalikan hash argon2id lengkap dengan parameter dan
// salt-nya, dalam format standar $argon2id$v=19$m=..,t=..,p=..$salt$hash.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLen {
		return "", fmt.Errorf("auth: kata sandi minimal %d karakter", MinPasswordLen)
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: gagal membangkitkan salt: %w", err)
	}

	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(sum)), nil
}

// VerifyPassword mencocokkan kata sandi dengan hash tersimpan.
//
// Parameter dibaca dari hash-nya sendiri, bukan dari konstanta di atas: hash
// yang dibuat sebelum biayanya dinaikkan harus tetap bisa diverifikasi.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrMalformedHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrMalformedHash
	}

	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrMalformedHash
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, ErrMalformedHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, ErrMalformedHash
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
