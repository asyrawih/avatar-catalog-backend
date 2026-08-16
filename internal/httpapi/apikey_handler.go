package httpapi

import (
	"net/http"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"slices"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

type apiKeyHandler struct {
	keys *service.APIKeys
}

// --- DTO --------------------------------------------------------------------

// apiKeyDTO sengaja tidak punya field untuk hash maupun token. Bentuk inilah
// yang dipakai daftar, dan daftar tidak boleh bisa memulihkan kredensial.
type apiKeyDTO struct {
	KeyID      string     `json:"keyId"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

// issuedKeyDTO adalah balasan POST /v1/keys — satu-satunya tempat token utuh
// pernah muncul.
type issuedKeyDTO struct {
	apiKeyDTO
	Token string `json:"token"`
}

// keyStatus meringkas tiga keadaan yang menentukan apakah kunci masih dipakai:
// dicabut, kedaluwarsa, atau aktif. Klien tidak perlu menghitungnya sendiri
// dari dua timestamp yang boleh null.
func keyStatus(key auth.Key, now time.Time) string {
	switch {
	case key.RevokedAt != nil:
		return "revoked"
	case key.ExpiresAt != nil && !now.Before(*key.ExpiresAt):
		return "expired"
	default:
		return "active"
	}
}

func newAPIKey(key auth.Key, now time.Time) apiKeyDTO {
	return apiKeyDTO{
		KeyID:      key.KeyID,
		Name:       key.Name,
		Scopes:     auth.ScopeStrings(key.Scopes),
		Status:     keyStatus(key, now),
		CreatedAt:  key.CreatedAt,
		ExpiresAt:  key.ExpiresAt,
		RevokedAt:  key.RevokedAt,
		LastUsedAt: key.LastUsedAt,
	}
}

// --- handler ----------------------------------------------------------------

// list menangani GET /v1/keys. Daftarnya tidak berhalaman: jumlah kunci di
// satu deployment dihitung dengan jari, dan cursor hanya akan menambah bagian
// yang bisa salah.
func (h *apiKeyHandler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.keys.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}

	now := time.Now().UTC()
	out := make([]apiKeyDTO, 0, len(rows))
	for _, key := range rows {
		out = append(out, newAPIKey(key, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

type issueKeyBody struct {
	Name string `json:"name"`
	// role ATAU scopes, tidak keduanya.
	Role   string   `json:"role"`
	Scopes []string `json:"scopes"`
	// expiresInHours wajib: jalur HTTP tidak menerbitkan kunci abadi.
	ExpiresInHours int    `json:"expiresInHours"`
	Env            string `json:"env"`
}

// create menangani POST /v1/keys. Balasannya memuat token utuh sekali; yang
// tersimpan hanya hash-nya, jadi token yang hilang harus diterbitkan ulang.
func (h *apiKeyHandler) create(w http.ResponseWriter, r *http.Request) {
	var body issueKeyBody
	if !decodeJSON(w, r, &body) {
		return
	}

	issued, err := h.keys.Issue(r.Context(), service.IssueKeyInput{
		Name:      body.Name,
		Role:      body.Role,
		Scopes:    body.Scopes,
		ExpiresIn: time.Duration(body.ExpiresInHours) * time.Hour,
		Env:       body.Env,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Location", "/v1/keys/"+issued.Key.KeyID)
	writeJSON(w, http.StatusCreated, issuedKeyDTO{
		apiKeyDTO: newAPIKey(issued.Key, time.Now().UTC()),
		Token:     issued.Token,
	})
}

type updateKeyBody struct {
	Name           *string `json:"name"`
	ExpiresInHours *int    `json:"expiresInHours"`
}

// update menangani PATCH /v1/keys/{keyId} — ganti nama atau geser masa
// berlaku. Scope tidak bisa diubah; lihat service.UpdateKeyInput.
func (h *apiKeyHandler) update(w http.ResponseWriter, r *http.Request) {
	var body updateKeyBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == nil && body.ExpiresInHours == nil {
		writeError(w, apierr.Unprocessable("empty_update", "Tidak ada yang diubah"))
		return
	}

	key, err := h.keys.Update(r.Context(), r.PathValue("keyId"), service.UpdateKeyInput{
		Name:           body.Name,
		ExpiresInHours: body.ExpiresInHours,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newAPIKey(key, time.Now().UTC()))
}

// purge menangani DELETE /v1/keys/{keyId}?hard=true — menghapus barisnya, bukan
// mencabut. Dipisah lewat parameter, bukan metode lain, supaya pencabutan tetap
// jadi jawaban bawaan dari DELETE: itu yang benar hampir selalu.
func (h *apiKeyHandler) remove(w http.ResponseWriter, r *http.Request) {
	hard, err := queryBool(r, "hard")
	if err != nil {
		writeError(w, err)
		return
	}

	if hard != nil && *hard {
		if err := h.keys.Delete(r.Context(), r.PathValue("keyId")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.keys.Revoke(r.Context(), r.PathValue("keyId")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listRoles menangani GET /v1/keys/roles — paket scope siap pakai, supaya
// penerbit memilih dari daftar dan bukan mengarang scope satu per satu.
func (h *apiKeyHandler) listRoles(w http.ResponseWriter, _ *http.Request) {
	type roleDTO struct {
		Role   string   `json:"role"`
		Scopes []string `json:"scopes"`
	}

	out := make([]roleDTO, 0, len(auth.Roles))
	for name, scopes := range auth.Roles {
		out = append(out, roleDTO{Role: name, Scopes: auth.ScopeStrings(scopes)})
	}
	// Map Go tidak berurutan; tanpa ini daftar role berubah urutan tiap
	// request dan klien yang me-render-nya ikut berkedip.
	slices.SortFunc(out, func(a, b roleDTO) int { return strings.Compare(a.Role, b.Role) })

	writeJSON(w, http.StatusOK, map[string]any{
		"data":              out,
		"allScopes":         auth.ScopeStrings(auth.AllScopes),
		"maxExpiresInHours": service.MaxKeyLifetimeHours,
	})
}
