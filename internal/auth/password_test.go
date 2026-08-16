package auth_test

import (
	"strings"
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
)

func TestHashPasswordBolakBalik(t *testing.T) {
	const password = "kata-sandi-yang-panjang"

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("hash memuat kata sandinya sendiri")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash = %q, ingin berawalan $argon2id$", hash)
	}

	ok, err := auth.VerifyPassword(password, hash)
	if err != nil || !ok {
		t.Errorf("VerifyPassword(benar) = %v, %v; ingin true, nil", ok, err)
	}

	ok, err = auth.VerifyPassword(password+"x", hash)
	if err != nil || ok {
		t.Errorf("VerifyPassword(salah) = %v, %v; ingin false, nil", ok, err)
	}
}

// Salt acak per hash: dua operator dengan kata sandi sama tidak boleh punya
// baris hash yang identik, karena itu membocorkan bahwa keduanya sama.
func TestHashPasswordMemakaiSaltBerbeda(t *testing.T) {
	a, err := auth.HashPassword("kata-sandi-yang-panjang")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	b, err := auth.HashPassword("kata-sandi-yang-panjang")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if a == b {
		t.Error("dua hash dari kata sandi yang sama ternyata identik")
	}
}

func TestKataSandiTerlaluPendekDitolak(t *testing.T) {
	if _, err := auth.HashPassword(strings.Repeat("x", auth.MinPasswordLen-1)); err == nil {
		t.Error("kata sandi di bawah batas minimum diterima")
	}
}

// Hash rusak harus dibedakan dari kata sandi salah: yang pertama kerusakan
// data yang perlu diselidiki, yang kedua kejadian sehari-hari.
func TestHashRusakBukanKataSandiSalah(t *testing.T) {
	for _, encoded := range []string{"", "bukan-hash", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA"} {
		if _, err := auth.VerifyPassword("kata-sandi-yang-panjang", encoded); err == nil {
			t.Errorf("VerifyPassword(%q) tidak melaporkan error", encoded)
		}
	}
}
