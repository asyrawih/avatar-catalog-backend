package auth

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Scope adalah izin yang melekat pada sebuah kunci API.
//
// Scope dipisah per kelompok kemampuan, bukan per endpoint: daftar per endpoint
// akan ikut berubah setiap kali rute ditambah, dan kunci yang sudah beredar
// pelan-pelan jadi kurang izin tanpa ada yang sadar.
type Scope string

// Scope yang dikenali.
const (
	// ScopeCatalogRead membaca outfit dan rig.
	ScopeCatalogRead Scope = "catalog:read"
	// ScopeCatalogWrite membuat dan mengubah outfit, rig, like, dan view.
	ScopeCatalogWrite Scope = "catalog:write"
	// ScopeTransactionsWrite mencatat transaksi pembelian.
	ScopeTransactionsWrite Scope = "transactions:write"
	// ScopeTransactionsRead membaca riwayat transaksi.
	ScopeTransactionsRead Scope = "transactions:read"
	// ScopeCashbackRead membaca saldo dan ledger cashback.
	ScopeCashbackRead Scope = "cashback:read"
	// ScopeCashbackRedeem membuat request redeem atas nama pemain.
	ScopeCashbackRedeem Scope = "cashback:redeem"
	// ScopeCashbackAdmin menuntaskan redeem, menarik cashback, dan menjadwalkan
	// event. Ini uang keluar — sengaja terpisah dari scope apa pun yang dipegang
	// game server.
	ScopeCashbackAdmin Scope = "cashback:admin"
	// ScopeActorAssert mengizinkan pemegang kunci menyatakan sedang bertindak
	// atas nama seorang pemain lewat header X-User-Id.
	//
	// Ini scope paling berbahaya di daftar ini: pemegangnya bisa menyamar
	// sebagai pemain mana pun. Hanya game server yang boleh memilikinya, karena
	// hanya dia yang tahu pemain mana yang benar-benar sedang bermain.
	ScopeActorAssert Scope = "actor:assert"
)

// AllScopes adalah seluruh scope yang dikenali, dipakai untuk memvalidasi
// masukan CLI.
var AllScopes = []Scope{
	ScopeCatalogRead, ScopeCatalogWrite,
	ScopeTransactionsWrite, ScopeTransactionsRead,
	ScopeCashbackRead, ScopeCashbackRedeem, ScopeCashbackAdmin,
	ScopeActorAssert,
}

// Role adalah paket scope siap pakai untuk satu jenis konsumen.
//
// Ada karena kesalahan paling sering pada sistem berbasis scope bukan scope-nya
// salah rancang, melainkan salah pilih saat menerbitkan kunci. Menerbitkan
// dengan --role membuat pilihan yang benar jadi pilihan yang paling gampang.
var Roles = map[string][]Scope{
	// game-server: Roblox server-side. Boleh bertindak atas nama pemain, tapi
	// TIDAK boleh menuntaskan redeem — itu jalur uang keluar.
	"game-server": {
		ScopeCatalogRead, ScopeCatalogWrite,
		ScopeTransactionsWrite, ScopeTransactionsRead,
		ScopeCashbackRead, ScopeCashbackRedeem,
		ScopeActorAssert,
	},
	// dashboard: tool internal tim. Boleh menuntaskan redeem dan menarik
	// cashback, tapi tidak boleh menyamar jadi pemain.
	"dashboard": {
		ScopeCatalogRead, ScopeTransactionsRead,
		ScopeCashbackRead, ScopeCashbackAdmin,
	},
	// ai: pengambil data latih. Baca saja, dan tidak menyentuh cashback sama
	// sekali — data popularitas tidak butuh akses ke jalur uang.
	"ai": {
		ScopeCatalogRead, ScopeTransactionsRead,
	},
	// public-read: calon API publik. Serendah mungkin: baca katalog saja.
	"public-read": {
		ScopeCatalogRead,
	},
}

// ParseScopes memvalidasi daftar scope dari masukan manusia.
func ParseScopes(raw []string) ([]Scope, error) {
	out := make([]Scope, 0, len(raw))
	for _, item := range raw {
		scope := Scope(strings.TrimSpace(item))
		if scope == "" {
			continue
		}
		if !slices.Contains(AllScopes, scope) {
			return nil, fmt.Errorf("auth: scope %q tidak dikenal", scope)
		}
		if !slices.Contains(out, scope) {
			out = append(out, scope)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("auth: kunci tanpa scope tidak berguna")
	}
	return out, nil
}

// Key adalah kunci API yang tersimpan, tanpa rahasianya.
type Key struct {
	KeyID      string
	Hash       []byte
	Name       string
	Scopes     []Scope
	CreatedAt  time.Time
	ExpiresAt  *time.Time // nil = tanpa masa berlaku
	RevokedAt  *time.Time // bukan nil = dicabut
	LastUsedAt *time.Time
}

// Usable melaporkan apakah kunci masih boleh dipakai pada waktu now.
func (k Key) Usable(now time.Time) bool {
	if k.RevokedAt != nil {
		return false
	}
	return k.ExpiresAt == nil || now.Before(*k.ExpiresAt)
}

// Has melaporkan apakah kunci memiliki scope tertentu.
func (k Key) Has(scope Scope) bool { return slices.Contains(k.Scopes, scope) }

// ScopeStrings mengembalikan scope sebagai []string untuk disimpan atau dicatat.
func ScopeStrings(scopes []Scope) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, string(s))
	}
	return out
}

// ScopesFromStrings membaca kembali scope dari penyimpanan. Nilai yang tidak
// dikenal dibuang, bukan menggagalkan pembacaan: scope yang dihapus dari kode
// jangan sampai membuat seluruh kunci lama tidak bisa dibaca.
func ScopesFromStrings(raw []string) []Scope {
	out := make([]Scope, 0, len(raw))
	for _, item := range raw {
		if scope := Scope(item); slices.Contains(AllScopes, scope) {
			out = append(out, scope)
		}
	}
	return out
}
