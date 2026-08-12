package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
)

func TestGenerateMenghasilkanTokenUnik(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := auth.Generate(auth.EnvLive)
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		if seen[token.Secret] {
			t.Fatal("Generate() mengulang token yang sama")
		}
		if seen[token.KeyID] {
			t.Fatal("Generate() mengulang keyId yang sama")
		}
		seen[token.Secret], seen[token.KeyID] = true, true
	}
}

func TestTokenBerbentukSesuaiSpesifikasi(t *testing.T) {
	token, err := auth.Generate(auth.EnvLive)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	parts := strings.Split(token.Secret, "_")
	if len(parts) != 4 {
		t.Fatalf("token = %q, ingin empat bagian dipisah '_'", token.Secret)
	}
	if parts[0] != "acb" || parts[1] != auth.EnvLive {
		t.Errorf("awalan token = %q_%q, ingin acb_live", parts[0], parts[1])
	}
	if parts[2] != token.KeyID {
		t.Errorf("keyId di token = %q, ingin %q", parts[2], token.KeyID)
	}
	// 32 byte base32 tanpa padding = 52 karakter.
	if len(parts[3]) != 52 {
		t.Errorf("panjang rahasia = %d karakter, ingin 52 (256 bit)", len(parts[3]))
	}
	// Pemisah bagian adalah "_", jadi alfabetnya tidak boleh memuatnya.
	if strings.ContainsAny(parts[2]+parts[3], "_-") {
		t.Errorf("keyId/rahasia memuat karakter pemisah: %q %q", parts[2], parts[3])
	}
}

// Token utuh tidak boleh bisa diturunkan dari apa yang tersimpan.
func TestHashTidakMemuatTokenAslinya(t *testing.T) {
	token, err := auth.Generate(auth.EnvLive)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if strings.Contains(string(token.Hash), token.Secret) {
		t.Error("hash memuat token aslinya")
	}
	if len(token.Hash) != 32 {
		t.Errorf("panjang hash = %d, ingin 32 (SHA-256)", len(token.Hash))
	}
}

func TestHashTokenKonsistenDanBerbedaPerToken(t *testing.T) {
	a, _ := auth.Generate(auth.EnvLive)
	b, _ := auth.Generate(auth.EnvLive)

	if !auth.Equal(auth.HashToken(a.Secret), a.Hash) {
		t.Error("HashToken() tidak menghasilkan hash yang sama dengan saat penerbitan")
	}
	if auth.Equal(auth.HashToken(a.Secret), b.Hash) {
		t.Error("dua token berbeda menghasilkan hash yang sama")
	}
}

func TestParseKeyIDMenolakBentukNgawur(t *testing.T) {
	valid, _ := auth.Generate(auth.EnvTest)

	cases := map[string]string{
		"kosong":            "",
		"tanpa pemisah":     "acbtestkeyidsecret",
		"bagian kurang":     "acb_test_keyid",
		"bagian kelebihan":  valid.Secret + "_lagi",
		"awalan salah":      strings.Replace(valid.Secret, "acb_", "xyz_", 1),
		"env tidak dikenal": strings.Replace(valid.Secret, "_test_", "_staging_", 1),
		"rahasia kosong":    "acb_test_keyid_",
	}
	for name, token := range cases {
		if _, err := auth.ParseKeyID(token); err == nil {
			t.Errorf("%s: ParseKeyID(%q) tidak error, ingin ditolak", name, token)
		}
	}

	got, err := auth.ParseKeyID(valid.Secret)
	if err != nil {
		t.Fatalf("ParseKeyID() token sah error = %v", err)
	}
	if got != valid.KeyID {
		t.Errorf("keyId = %q, ingin %q", got, valid.KeyID)
	}
}

func TestGenerateMenolakEnvTidakDikenal(t *testing.T) {
	if _, err := auth.Generate("staging"); err == nil {
		t.Error("Generate(\"staging\") tidak error, ingin ditolak")
	}
}

func TestKeyUsable(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	lalu := now.Add(-time.Hour)
	nanti := now.Add(time.Hour)

	cases := []struct {
		name string
		key  auth.Key
		want bool
	}{
		{"tanpa batas", auth.Key{}, true},
		{"belum kedaluwarsa", auth.Key{ExpiresAt: &nanti}, true},
		{"sudah kedaluwarsa", auth.Key{ExpiresAt: &lalu}, false},
		{"dicabut", auth.Key{RevokedAt: &lalu}, false},
		{"dicabut walau belum kedaluwarsa", auth.Key{RevokedAt: &lalu, ExpiresAt: &nanti}, false},
	}
	for _, tc := range cases {
		if got := tc.key.Usable(now); got != tc.want {
			t.Errorf("%s: Usable() = %v, ingin %v", tc.name, got, tc.want)
		}
	}
}

func TestParseScopesMenolakYangTidakDikenal(t *testing.T) {
	if _, err := auth.ParseScopes([]string{"catalog:read", "catalog:hapus-semua"}); err == nil {
		t.Error("ParseScopes() menerima scope yang tidak dikenal")
	}
	if _, err := auth.ParseScopes(nil); err == nil {
		t.Error("ParseScopes(nil) tidak error; kunci tanpa scope tidak berguna")
	}

	scopes, err := auth.ParseScopes([]string{"catalog:read", " catalog:read ", "catalog:write"})
	if err != nil {
		t.Fatalf("ParseScopes() error = %v", err)
	}
	if len(scopes) != 2 {
		t.Errorf("scopes = %v, ingin duplikat dibuang dan spasi dipangkas", scopes)
	}
}

// Pembagian scope yang paling penting: game server tidak boleh menyentuh jalur
// uang keluar, dan yang bukan game server tidak boleh menyamar jadi pemain.
func TestRoleGameServerTidakBisaMenuntaskanRedeem(t *testing.T) {
	gameServer := auth.Key{Scopes: auth.Roles["game-server"]}

	if gameServer.Has(auth.ScopeCashbackAdmin) {
		t.Error("role game-server punya cashback:admin; itu jalur uang keluar")
	}
	if !gameServer.Has(auth.ScopeCashbackRedeem) {
		t.Error("role game-server tidak bisa membuat request redeem")
	}
	if !gameServer.Has(auth.ScopeActorAssert) {
		t.Error("role game-server tidak bisa bertindak atas nama pemain")
	}
}

func TestRoleSelainGameServerTidakBisaMenyamarJadiPemain(t *testing.T) {
	for _, name := range []string{"dashboard", "ai", "public-read"} {
		key := auth.Key{Scopes: auth.Roles[name]}
		if key.Has(auth.ScopeActorAssert) {
			t.Errorf("role %s punya actor:assert; hanya game server yang boleh menyamar jadi pemain", name)
		}
	}
}

func TestRoleAIHanyaBaca(t *testing.T) {
	ai := auth.Key{Scopes: auth.Roles["ai"]}

	for _, scope := range []auth.Scope{
		auth.ScopeCatalogWrite, auth.ScopeTransactionsWrite,
		auth.ScopeCashbackRedeem, auth.ScopeCashbackAdmin, auth.ScopeActorAssert,
	} {
		if ai.Has(scope) {
			t.Errorf("role ai punya %s; pengambil data latih cukup baca saja", scope)
		}
	}
	if !ai.Has(auth.ScopeCatalogRead) {
		t.Error("role ai tidak bisa membaca katalog")
	}
}

// Semua scope yang dipakai role harus ada di AllScopes, kalau tidak
// ScopesFromStrings akan membuangnya diam-diam saat dibaca dari database.
func TestScopeDalamRoleSemuanyaDikenali(t *testing.T) {
	for name, scopes := range auth.Roles {
		roundTrip := auth.ScopesFromStrings(auth.ScopeStrings(scopes))
		if len(roundTrip) != len(scopes) {
			t.Errorf("role %s: %d scope hilang saat dibaca ulang dari penyimpanan",
				name, len(scopes)-len(roundTrip))
		}
	}
}
