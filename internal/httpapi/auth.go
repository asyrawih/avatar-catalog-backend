package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Caller adalah hasil autentikasi satu request: kunci mana yang dipakai, dan
// pemain mana yang sedang diwakilinya.
type Caller struct {
	// KeyID dan KeyName kosong berarti request tidak terautentikasi. Itu hanya
	// mungkin pada mode tanpa kunci (lihat UnverifiedActorAuth).
	KeyID   string
	KeyName string
	Scopes  []auth.Scope
	// Actor adalah pemain yang diwakili. Kosong berarti pemanggil bertindak
	// sebagai dirinya sendiri, bukan atas nama pemain.
	Actor service.Actor
}

// Has melaporkan apakah pemanggil punya scope tertentu.
func (c Caller) Has(scope auth.Scope) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Authenticator menentukan siapa pemanggil sebuah request.
type Authenticator interface {
	Authenticate(r *http.Request) (Caller, error)
}

// errUnauthenticated adalah SATU-SATUNYA balasan untuk semua kegagalan
// autentikasi: bentuk token salah, keyId tidak ada, hash tidak cocok, kunci
// dicabut, kunci kedaluwarsa.
//
// Membedakannya akan memberi tahu penyerang seberapa jauh tebakannya sudah
// benar — "kunci dicabut" mengonfirmasi bahwa tokennya pernah sah. Alasan
// sebenarnya tetap dicatat di log server.
func errUnauthenticated() error {
	return apierr.Unauthorized("unauthenticated", "Kunci API tidak valid")
}

// errActorForbidden dipakai kunci maupun sesi yang mencoba bertindak atas nama
// pemain tanpa scope actor:assert.
func errActorForbidden() error {
	return apierr.Forbidden("actor_assert_forbidden",
		"Kredensial ini tidak boleh bertindak atas nama pemain")
}

// KeyAuth memverifikasi Bearer token terhadap kunci yang tersimpan.
type KeyAuth struct {
	keys   store.APIKeys
	logger *slog.Logger
	now    func() time.Time
}

var _ Authenticator = (*KeyAuth)(nil)

// NewKeyAuth merangkai authenticator berbasis kunci tersimpan.
func NewKeyAuth(keys store.APIKeys, logger *slog.Logger) *KeyAuth {
	if logger == nil {
		logger = slog.Default()
	}
	return &KeyAuth{keys: keys, logger: logger, now: func() time.Time { return time.Now().UTC() }}
}

// Authenticate memverifikasi token lalu menentukan pemain yang diwakili.
func (a *KeyAuth) Authenticate(r *http.Request) (Caller, error) {
	raw, ok := bearerToken(r)
	if !ok {
		return Caller{}, errUnauthenticated()
	}

	keyID, err := auth.ParseKeyID(raw)
	if err != nil {
		return Caller{}, errUnauthenticated()
	}

	key, err := a.keys.ByKeyID(r.Context(), keyID)
	if errors.Is(err, store.ErrNotFound) {
		a.logger.Warn("kunci API tidak dikenal", "keyId", keyID)
		return Caller{}, errUnauthenticated()
	}
	if err != nil {
		return Caller{}, err
	}

	// Hash dicocokkan lebih dulu, sebelum status dicabut/kedaluwarsa diperiksa.
	// Urutan ini penting: memeriksa status duluan membuat lama balasan berbeda
	// antara keyId yang ada dan yang tidak, sehingga keyId yang sah bisa
	// dipetakan tanpa pernah menebak rahasianya.
	if !auth.Equal(key.Hash, auth.HashToken(raw)) {
		a.logger.Warn("hash kunci API tidak cocok", "keyId", keyID, "name", key.Name)
		return Caller{}, errUnauthenticated()
	}
	if !key.Usable(a.now()) {
		a.logger.Warn("kunci API dipakai setelah tidak berlaku",
			"keyId", keyID, "name", key.Name,
			"revokedAt", key.RevokedAt, "expiresAt", key.ExpiresAt)
		return Caller{}, errUnauthenticated()
	}

	// Catatan operasional, bukan bagian keputusan autentikasi: kegagalannya
	// tidak boleh menjatuhkan request yang tokennya sah.
	if err := a.keys.TouchLastUsed(r.Context(), keyID, a.now()); err != nil {
		a.logger.Warn("gagal mencatat pemakaian kunci", "keyId", keyID, "err", err)
	}

	caller := Caller{KeyID: key.KeyID, KeyName: key.Name, Scopes: key.Scopes}
	actor, err := actorHeader(r)
	if err != nil {
		return Caller{}, err
	}
	if actor.Present {
		// Menyatakan diri sebagai pemain adalah kemampuan tersendiri. Tanpa
		// scope ini, kunci dashboard atau AI yang bocor tetap tidak bisa
		// menyukai, menukar cashback, atau membuat outfit atas nama siapa pun.
		if !caller.Has(auth.ScopeActorAssert) {
			return Caller{}, errActorForbidden()
		}
		caller.Actor = actor
	}
	return caller, nil
}

// UnverifiedActorAuth adalah mode tanpa kunci: identitas pemain dibaca apa
// adanya dari X-User-Id dan tidak ada token yang diperiksa.
//
// Dipakai hanya saat penyimpanan kunci tidak dirakit (mode in-memory untuk
// pengembangan). Pemanggil dianggap punya seluruh scope, karena di mode ini
// memang tidak ada batas yang bisa ditegakkan — jangan pernah dipakai di
// produksi. cmd/server menolak start di APP_ENV=production tanpa kunci.
type UnverifiedActorAuth struct{}

var _ Authenticator = UnverifiedActorAuth{}

// Authenticate membaca X-User-Id bila ada.
func (UnverifiedActorAuth) Authenticate(r *http.Request) (Caller, error) {
	actor, err := actorHeader(r)
	if err != nil {
		return Caller{}, err
	}
	return Caller{KeyName: "unverified", Scopes: auth.AllScopes, Actor: actor}, nil
}

// bearerToken mengambil token dari header Authorization. Nama skema-nya
// dibandingkan tanpa peduli huruf besar-kecil, sesuai RFC 7235.
func bearerToken(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// actorHeader membaca pemain yang diwakili dari X-User-Id.
func actorHeader(r *http.Request) (service.Actor, error) {
	raw := strings.TrimSpace(r.Header.Get("X-User-Id"))
	if raw == "" {
		return service.Actor{}, nil
	}

	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || userID <= 0 {
		return service.Actor{}, apierr.BadRequest("invalid_actor", "Header X-User-Id tidak valid")
	}
	return service.Actor{UserID: userID, Present: true}, nil
}

type callerCtxKey struct{}

// authenticate memasang hasil Authenticator ke context request.
func authenticate(a Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, err := a.Authenticate(r)
			if err != nil {
				writeError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerCtxKey{}, caller)))
		})
	}
}

// requireScope menolak request dari kunci yang tidak punya scope yang dibutuhkan.
//
// Dipasang per rute, bukan diperiksa di dalam handler: rute baru yang lupa
// dibungkus akan terlihat jelas di router, sedangkan pemeriksaan yang lupa
// ditulis di dalam handler tidak kelihatan dari mana pun.
func requireScope(scope auth.Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !callerFrom(r.Context()).Has(scope) {
				writeError(w, apierr.Forbidden("insufficient_scope",
					"Kunci ini tidak punya scope "+string(scope)))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// callerFrom mengembalikan pemanggil request; zero value berarti tanpa kunci.
func callerFrom(ctx context.Context) Caller {
	caller, _ := ctx.Value(callerCtxKey{}).(Caller)
	return caller
}

// actorFrom mengembalikan pemain yang diwakili request.
func actorFrom(ctx context.Context) service.Actor {
	return callerFrom(ctx).Actor
}
