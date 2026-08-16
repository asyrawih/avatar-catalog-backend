package httpapi

import (
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

type dashboardAuthHandler struct {
	dashboard *service.DashboardAuth
	cookie    cookieConfig
}

// --- DTO --------------------------------------------------------------------

// dashboardUserDTO tidak punya field untuk hash kata sandi, dan itu bukan
// kelalaian: bentuk inilah yang dipakai /v1/auth/me maupun daftar operator.
type dashboardUserDTO struct {
	UserID      string     `json:"userId"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Scopes      []string   `json:"scopes"`
	Disabled    bool       `json:"disabled"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
}

func newDashboardUser(user auth.User) dashboardUserDTO {
	return dashboardUserDTO{
		UserID:      user.UserID,
		Email:       user.Email,
		Name:        user.Name,
		Scopes:      auth.ScopeStrings(auth.SessionScopes),
		Disabled:    !user.Active(),
		CreatedAt:   user.CreatedAt,
		LastLoginAt: user.LastLoginAt,
	}
}

// --- handler ----------------------------------------------------------------

type loginBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// login menangani POST /v1/auth/login. Token sesi hanya dikirim sebagai cookie
// httpOnly, TIDAK di body: token yang bisa dibaca JavaScript halaman berarti
// satu XSS cukup untuk mencurinya, dan itu yang justru dihindari dengan
// memakai cookie.
func (h *dashboardAuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var body loginBody
	if !decodeJSON(w, r, &body) {
		return
	}

	result, err := h.dashboard.Login(r.Context(), body.Email, body.Password, r.UserAgent())
	if err != nil {
		writeError(w, err)
		return
	}

	h.cookie.set(w, result.Token, result.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":      newDashboardUser(result.User),
		"expiresAt": result.ExpiresAt,
	})
}

// logout menangani POST /v1/auth/logout. Idempoten, dan cookie tetap dihapus
// walau sesinya sudah mati — klien yang memanggilnya menginginkan satu hal:
// setelah ini, browser saya tidak lagi membawa sesi.
func (h *dashboardAuthHandler) logout(w http.ResponseWriter, r *http.Request) {
	if token, ok := h.cookie.token(r); ok {
		if err := h.dashboard.Logout(r.Context(), token); err != nil {
			writeError(w, err)
			return
		}
	}
	h.cookie.clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// me menangani GET /v1/auth/me — dipakai dashboard saat dimuat untuk tahu
// apakah sesinya masih hidup, tanpa perlu menebak dari kegagalan request lain.
func (h *dashboardAuthHandler) me(w http.ResponseWriter, r *http.Request) {
	token, ok := h.cookie.token(r)
	if !ok {
		writeError(w, apierr.Unauthorized("no_session", "Belum login"))
		return
	}

	user, session, err := h.dashboard.Authenticate(r.Context(), token)
	if err != nil {
		// Sesi yang sudah tidak berlaku ikut membuang cookienya, supaya browser
		// berhenti mengirim token mati di tiap request berikutnya.
		h.cookie.clear(w)
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user":      newDashboardUser(user),
		"expiresAt": session.ExpiresAt,
	})
}

// --- manajemen operator -----------------------------------------------------

// listUsers menangani GET /v1/auth/users.
func (h *dashboardAuthHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.dashboard.ListUsers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]dashboardUserDTO, 0, len(rows))
	for _, user := range rows {
		out = append(out, newDashboardUser(user))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

type createUserBody struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// createUser menangani POST /v1/auth/users.
func (h *dashboardAuthHandler) createUser(w http.ResponseWriter, r *http.Request) {
	var body createUserBody
	if !decodeJSON(w, r, &body) {
		return
	}

	user, err := h.dashboard.CreateUser(r.Context(), service.CreateUserInput{
		Email:    body.Email,
		Name:     body.Name,
		Password: body.Password,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Location", "/v1/auth/users/"+user.UserID)
	writeJSON(w, http.StatusCreated, newDashboardUser(user))
}

type updateUserBody struct {
	// Semua opsional; nil berarti tidak diubah.
	Disabled *bool   `json:"disabled"`
	Password *string `json:"password"`
	Email    *string `json:"email"`
	Name     *string `json:"name"`
}

// updateUser menangani PATCH /v1/auth/users/{userId} — menonaktifkan,
// mengaktifkan kembali, atau mengganti kata sandi. Keduanya mematikan sesi
// operator itu.
func (h *dashboardAuthHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	var body updateUserBody
	if !decodeJSON(w, r, &body) {
		return
	}

	userID := r.PathValue("userId")
	if body.Disabled == nil && body.Password == nil && body.Email == nil && body.Name == nil {
		writeError(w, apierr.Unprocessable("empty_update", "Tidak ada yang diubah"))
		return
	}

	// Identitas diurus lebih dulu supaya kegagalannya (mis. email bentrok)
	// tidak menyisakan kata sandi yang sudah terlanjur berganti.
	if body.Email != nil || body.Name != nil {
		if _, err := h.dashboard.UpdateProfile(r.Context(), userID, service.UpdateProfileInput{
			Email: body.Email,
			Name:  body.Name,
		}); err != nil {
			writeError(w, err)
			return
		}
	}
	if body.Password != nil {
		if err := h.dashboard.ChangePassword(r.Context(), userID, *body.Password); err != nil {
			writeError(w, err)
			return
		}
	}
	if body.Disabled != nil {
		if err := h.dashboard.SetDisabled(r.Context(), userID, *body.Disabled); err != nil {
			writeError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeUser menangani DELETE /v1/auth/users/{userId}.
//
// Operator yang sedang login tidak bisa menghapus dirinya sendiri — service
// yang menolaknya; di sini cukup diberi tahu siapa pemanggilnya.
func (h *dashboardAuthHandler) removeUser(w http.ResponseWriter, r *http.Request) {
	var selfID string
	if token, ok := h.cookie.token(r); ok {
		if user, _, err := h.dashboard.Authenticate(r.Context(), token); err == nil {
			selfID = user.UserID
		}
	}

	if err := h.dashboard.DeleteUser(r.Context(), r.PathValue("userId"), selfID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
