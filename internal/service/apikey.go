package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// maxKeyNameLen membatasi nama kunci. Nama ini ikut tercatat di log tiap
// request, jadi panjangnya perlu dijaga.
const maxKeyNameLen = 120

// maxKeyLifetime membatasi masa berlaku yang boleh diminta lewat API.
//
// CLI mengizinkan kunci tanpa masa berlaku; jalur HTTP tidak. Kunci yang
// diterbitkan lewat browser lahir dari sesi yang bisa dibajak, dan yang paling
// membatasi kerusakannya adalah kunci itu mati sendiri. Kunci abadi tetap bisa
// dibuat, tapi harus lewat CLI di mesin yang memegang DATABASE_URL.
const maxKeyLifetime = 365 * 24 * time.Hour

// MaxKeyLifetimeHours adalah maxKeyLifetime dalam jam, dipakai lapisan HTTP
// untuk memberi tahu klien batasnya tanpa menebak.
const MaxKeyLifetimeHours = int(maxKeyLifetime / time.Hour)

// IssueKeyInput adalah muatan POST /v1/keys.
type IssueKeyInput struct {
	Name string
	// Role, bila diisi, menggantikan Scopes. Salah satu wajib ada.
	Role   string
	Scopes []string
	// ExpiresIn wajib dan tidak boleh melebihi maxKeyLifetime.
	ExpiresIn time.Duration
	Env       string
}

// IssuedKey adalah kunci baru beserta tokennya. Token hanya ada di sini, satu
// kali: yang tersimpan cuma hash-nya.
type IssuedKey struct {
	Key   auth.Key
	Token string
}

// APIKeys mengelola kunci API lewat HTTP.
//
// Seluruh operasinya butuh scope keys:admin, dan pemegang scope itu bisa
// mencetak kunci dengan scope apa pun — termasuk keys:admin lagi. Service ini
// karena itu tidak menambah kelonggaran apa pun di atas store: tidak ada
// pencarian by-name, tidak ada pembacaan hash, dan token tidak pernah
// disimpan.
type APIKeys struct {
	keys store.APIKeys
	now  func() time.Time
}

// NewAPIKeys merangkai service kunci API.
func NewAPIKeys(keys store.APIKeys) *APIKeys {
	return &APIKeys{
		keys: keys,
		now:  func() time.Time { return time.Now().UTC() },
	}
}

// List mengembalikan seluruh kunci, terbaru dulu. Hash tidak pernah ikut —
// pemanggil hanya melihat metadata.
func (s *APIKeys) List(ctx context.Context) ([]auth.Key, error) {
	return s.keys.List(ctx)
}

// Issue menerbitkan kunci baru dan mengembalikan tokennya sekali.
func (s *APIKeys) Issue(ctx context.Context, in IssueKeyInput) (IssuedKey, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return IssuedKey{}, apierr.Unprocessable("missing_name", "Field name wajib diisi")
	}
	if len(name) > maxKeyNameLen {
		return IssuedKey{}, apierr.Unprocessable("name_too_long", "Field name terlalu panjang")
	}

	scopes, err := s.resolveScopes(in.Role, in.Scopes)
	if err != nil {
		return IssuedKey{}, err
	}

	if in.ExpiresIn <= 0 {
		return IssuedKey{}, apierr.Unprocessable("missing_expires_in",
			"Field expiresInHours wajib diisi; kunci tanpa masa berlaku hanya bisa lewat CLI")
	}
	if in.ExpiresIn > maxKeyLifetime {
		return IssuedKey{}, apierr.Unprocessable("expires_in_too_long",
			"Masa berlaku maksimal 365 hari")
	}

	env := in.Env
	if env == "" {
		env = auth.EnvLive
	}
	token, err := auth.Generate(env)
	if err != nil {
		return IssuedKey{}, apierr.Unprocessable("invalid_env", "Field env harus live atau test")
	}

	now := s.now()
	expiresAt := now.Add(in.ExpiresIn)
	key := auth.Key{
		KeyID:     token.KeyID,
		Hash:      token.Hash,
		Name:      name,
		Scopes:    scopes,
		CreatedAt: now,
		ExpiresAt: &expiresAt,
	}
	if err := s.keys.Create(ctx, key); err != nil {
		return IssuedKey{}, err
	}

	// Hash tidak ikut keluar dari service walau baris di database memilikinya.
	key.Hash = nil
	return IssuedKey{Key: key, Token: token.Secret}, nil
}

// UpdateKeyInput adalah muatan PATCH /v1/keys/{keyId}. Field nil = tidak
// diubah.
//
// Scope sengaja TIDAK bisa diubah. Kunci yang beredar dengan izin yang
// diam-diam bertambah adalah eskalasi yang tak terlihat dari sisi pemakainya;
// menaikkan izin harus lewat penerbitan kunci baru, yang tokennya berganti dan
// pemiliknya sadar menerimanya.
type UpdateKeyInput struct {
	Name *string
	// ExpiresInHours menggeser masa berlaku dihitung dari SEKARANG, bukan dari
	// waktu penerbitan — itu yang dimaksud orang saat memperpanjang kunci. 0
	// berarti mencabut masa berlaku, dan itu ditolak: jalur HTTP tidak
	// menerbitkan maupun membuat kunci abadi.
	ExpiresInHours *int
}

// Update mengubah nama dan/atau masa berlaku kunci. Rahasianya tidak berubah,
// jadi pemakainya tidak perlu memasang ulang apa pun.
func (s *APIKeys) Update(ctx context.Context, keyID string, in UpdateKeyInput) (auth.Key, error) {
	if strings.TrimSpace(keyID) == "" {
		return auth.Key{}, apierr.BadRequest("missing_key_id", "keyId wajib diisi")
	}

	key, err := s.keys.ByKeyID(ctx, keyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return auth.Key{}, apierr.NotFound("key_not_found", "Kunci tidak ditemukan")
		}
		return auth.Key{}, err
	}

	name := key.Name
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if name == "" {
			return auth.Key{}, apierr.Unprocessable("missing_name", "Field name tidak boleh kosong")
		}
		if len(name) > maxKeyNameLen {
			return auth.Key{}, apierr.Unprocessable("name_too_long", "Field name terlalu panjang")
		}
	}

	expiresAt := key.ExpiresAt
	if in.ExpiresInHours != nil {
		hours := *in.ExpiresInHours
		if hours <= 0 {
			return auth.Key{}, apierr.Unprocessable("missing_expires_in",
				"expiresInHours harus lebih dari nol; kunci tanpa masa berlaku hanya bisa lewat CLI")
		}
		if time.Duration(hours)*time.Hour > maxKeyLifetime {
			return auth.Key{}, apierr.Unprocessable("expires_in_too_long",
				"Masa berlaku maksimal 365 hari")
		}
		at := s.now().Add(time.Duration(hours) * time.Hour)
		expiresAt = &at
	}

	if err := s.keys.Update(ctx, keyID, name, expiresAt); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return auth.Key{}, apierr.NotFound("key_not_found", "Kunci tidak ditemukan")
		}
		return auth.Key{}, err
	}

	key.Name, key.ExpiresAt, key.Hash = name, expiresAt, nil
	return key, nil
}

// Delete menghapus baris kunci sepenuhnya.
//
// Berbeda dari Revoke yang menyisakan jejak — nama, scope, pemakaian terakhir —
// dan itulah yang biasanya dibutuhkan saat menelusuri insiden. Delete untuk
// kunci yang memang salah dibuat dan tidak pernah dipakai; untuk mematikan
// kunci yang beredar, Revoke pilihan yang benar.
func (s *APIKeys) Delete(ctx context.Context, keyID string) error {
	if strings.TrimSpace(keyID) == "" {
		return apierr.BadRequest("missing_key_id", "keyId wajib diisi")
	}
	if err := s.keys.Delete(ctx, keyID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NotFound("key_not_found", "Kunci tidak ditemukan")
		}
		return err
	}
	return nil
}

// Revoke mencabut kunci. Idempoten di store, jadi mencabut dua kali bukan
// error — hasil akhirnya sama.
func (s *APIKeys) Revoke(ctx context.Context, keyID string) error {
	if strings.TrimSpace(keyID) == "" {
		return apierr.BadRequest("missing_key_id", "keyId wajib diisi")
	}
	if err := s.keys.Revoke(ctx, keyID, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NotFound("key_not_found", "Kunci tidak ditemukan")
		}
		return err
	}
	return nil
}

// resolveScopes menerima role ATAU daftar scope, tidak keduanya sekaligus:
// menerima keduanya berarti harus memilih mana yang menang, dan pilihan itu
// tidak akan sama dengan yang dibayangkan pemanggil.
func (s *APIKeys) resolveScopes(role string, raw []string) ([]auth.Scope, error) {
	role = strings.TrimSpace(role)
	if role != "" && len(raw) > 0 {
		return nil, apierr.Unprocessable("ambiguous_scopes", "Isi role atau scopes, bukan keduanya")
	}

	if role != "" {
		scopes, ok := auth.Roles[role]
		if !ok {
			return nil, apierr.Unprocessable("unknown_role", "Role tidak dikenal")
		}
		return slices.Clone(scopes), nil
	}

	scopes, err := auth.ParseScopes(raw)
	if err != nil {
		return nil, apierr.Unprocessable("invalid_scopes", "Scope tidak dikenal atau kosong")
	}
	return scopes, nil
}
