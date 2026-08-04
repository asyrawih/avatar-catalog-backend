package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

// Authenticator menentukan siapa pemanggil sebuah request.
//
// Autentikasi asli sengaja ditunda. Seluruh jalur sudah lewat sini, jadi
// menggantinya nanti cukup dengan menukar implementasi di NewRouter — handler
// dan service tidak perlu berubah.
type Authenticator interface {
	Authenticate(r *http.Request) (service.Actor, error)
}

// UnverifiedActorAuth adalah implementasi sementara: identitas diambil apa
// adanya dari header X-User-Id dan token Bearer TIDAK diverifikasi.
//
// Cukup untuk mengembangkan dan menguji jalur kepemilikan (403 not_owner),
// tapi jangan dipakai di produksi.
type UnverifiedActorAuth struct{}

var _ Authenticator = UnverifiedActorAuth{}

// Authenticate membaca X-User-Id bila ada. Request tanpa header itu tetap
// diteruskan sebagai pemanggil tanpa identitas.
func (UnverifiedActorAuth) Authenticate(r *http.Request) (service.Actor, error) {
	raw := strings.TrimSpace(r.Header.Get("X-User-Id"))
	if raw == "" {
		return service.Actor{}, nil
	}

	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || userID <= 0 {
		return service.Actor{}, apierr.Unauthorized("unauthenticated", "Header X-User-Id tidak valid")
	}
	return service.Actor{UserID: userID, Present: true}, nil
}

// StaticTokenAuth memeriksa Bearer token terhadap daftar token tetap.
//
// Ini bukan autentikasi pemain, melainkan kunci antar-layanan (game server ke
// backend). Dipakai kalau AUTH_TOKENS diisi; kalau kosong, router memakai
// UnverifiedActorAuth.
type StaticTokenAuth struct {
	tokens map[string]struct{}
}

var _ Authenticator = (*StaticTokenAuth)(nil)

// NewStaticTokenAuth membuat authenticator dari daftar token yang diizinkan.
func NewStaticTokenAuth(tokens []string) *StaticTokenAuth {
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if token = strings.TrimSpace(token); token != "" {
			set[token] = struct{}{}
		}
	}
	return &StaticTokenAuth{tokens: set}
}

// Authenticate menolak request tanpa Bearer token yang dikenal.
func (a *StaticTokenAuth) Authenticate(r *http.Request) (service.Actor, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return service.Actor{}, apierr.Unauthorized("unauthenticated", "Bearer token tidak valid")
	}
	if _, known := a.tokens[strings.TrimSpace(token)]; !known {
		return service.Actor{}, apierr.Unauthorized("unauthenticated", "Bearer token tidak valid")
	}
	// Token layanan dipercaya, identitas pemain tetap dari header sampai
	// autentikasi pemain sungguhan dipasang.
	return UnverifiedActorAuth{}.Authenticate(r)
}

type actorCtxKey struct{}

// authenticate memasang hasil Authenticator ke context request.
func authenticate(a Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, err := a.Authenticate(r)
			if err != nil {
				writeError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorCtxKey{}, actor)))
		})
	}
}

// actorFrom mengembalikan pemanggil request; zero value berarti anonim.
func actorFrom(ctx context.Context) service.Actor {
	actor, _ := ctx.Value(actorCtxKey{}).(service.Actor)
	return actor
}
