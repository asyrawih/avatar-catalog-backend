package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

// sessionCookie adalah nama cookie sesi dashboard.
//
// Awalan __Host- bukan hiasan: browser hanya menerima cookie dengan awalan itu
// kalau Secure aktif, Path=/, dan TANPA atribut Domain — sehingga subdomain
// lain tidak bisa menuliskannya. Itu menutup session fixation lewat subdomain
// yang dikuasai penyerang.
//
// Konsekuensinya cookie ini tidak jalan di http:// biasa. Untuk pengembangan
// lokal, nama dan atribut Secure-nya diturunkan — lihat sessionCookieConfig.
const (
	sessionCookieSecure = "__Host-acb_session"
	sessionCookieDev    = "acb_session"
)

// cookieConfig menentukan bentuk cookie sesi menurut lingkungan.
type cookieConfig struct {
	name   string
	secure bool
}

// NewCookieConfig memilih bentuk cookie. secure=false hanya untuk http://
// lokal; di produksi selalu true.
func NewCookieConfig(secure bool) cookieConfig {
	if secure {
		return cookieConfig{name: sessionCookieSecure, secure: true}
	}
	return cookieConfig{name: sessionCookieDev, secure: false}
}

// set menuliskan cookie sesi.
//
// SameSite=Lax, bukan None: dashboard memanggil API-nya lewat origin yang sama
// (dev server Vite di lokal, rewrite Vercel di produksi), jadi cookie tidak
// perlu ikut di request lintas situs — dan tidak ikutnya itulah yang membuat
// CSRF dari situs lain tidak bisa membawa sesi ini.
func (c cookieConfig) set(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.name,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clear menghapus cookie sesi. Nilainya dikosongkan DAN MaxAge negatif:
// browser lama hanya mengerti salah satunya.
func (c cookieConfig) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// token membaca token sesi dari request.
func (c cookieConfig) token(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(c.name)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

// SessionAuth menerima DUA bentuk kredensial: cookie sesi dashboard dan kunci
// API. Cookie diperiksa lebih dulu, lalu jatuh ke kunci.
//
// Digabung dalam satu Authenticator, bukan dua rute terpisah, supaya seluruh
// endpoint /v1 melayani keduanya tanpa satu pun handler perlu tahu bedanya —
// dan supaya penegakan scope tetap satu tempat: daftar rute di router.
type SessionAuth struct {
	dashboard *service.DashboardAuth
	keys      Authenticator
	cookie    cookieConfig
	logger    *slog.Logger
}

var _ Authenticator = (*SessionAuth)(nil)

// NewSessionAuth membungkus authenticator kunci dengan jalur sesi dashboard.
func NewSessionAuth(dashboard *service.DashboardAuth, keys Authenticator, cookie cookieConfig, logger *slog.Logger) *SessionAuth {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionAuth{dashboard: dashboard, keys: keys, cookie: cookie, logger: logger}
}

// Authenticate mencoba cookie sesi lebih dulu, lalu kunci API.
func (a *SessionAuth) Authenticate(r *http.Request) (Caller, error) {
	token, ok := a.cookie.token(r)
	if !ok {
		return a.keys.Authenticate(r)
	}

	user, session, err := a.dashboard.Authenticate(r.Context(), token)
	if err != nil {
		// Cookie basi tidak boleh mengunci pemanggil yang JUGA membawa kunci
		// API yang sah — mis. halaman yang sesinya kedaluwarsa tapi requestnya
		// memakai kunci. Kalau tidak ada kunci sama sekali, barulah kegagalan
		// sesi ini yang dilaporkan.
		if _, hasKey := bearerToken(r); hasKey {
			return a.keys.Authenticate(r)
		}
		a.logger.Warn("sesi dashboard ditolak", "err", err)
		return Caller{}, err
	}

	caller := Caller{
		KeyID:   session.SessionID,
		KeyName: "session:" + user.Email,
		Scopes:  auth.SessionScopes,
	}

	// Sesi dashboard boleh bertindak atas nama pemain karena SessionScopes
	// memuat actor:assert. Pemeriksaannya tetap lewat jalur yang sama dengan
	// kunci API supaya aturannya cuma ada satu.
	actor, err := actorHeader(r)
	if err != nil {
		return Caller{}, err
	}
	if actor.Present {
		if !caller.Has(auth.ScopeActorAssert) {
			return Caller{}, errActorForbidden()
		}
		caller.Actor = actor
	}
	return caller, nil
}
