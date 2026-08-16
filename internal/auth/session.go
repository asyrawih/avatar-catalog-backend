package auth

import (
	"strings"
	"time"
)

// Bentuk token sesi: acbs_<sessionId>_<secret>
//
// Dibedakan dari kunci API (acb_...) supaya token yang tertukar ditolak karena
// bentuknya, bukan karena kebetulan tidak ketemu di tabel — dan supaya pemindai
// rahasia bisa mengenali keduanya terpisah.
//
// Sama seperti kunci API, sessionId ada di dalam token supaya verifikasi cukup
// satu lookup indeks, dan yang tersimpan hanya SHA-256 dari token utuh: 256 bit
// acak tidak butuh KDF mahal.
const (
	sessionPrefix    = "acbs"
	sessionIDBytes   = 8
	sessionSecretLen = 32
)

// SessionLifetime adalah umur sesi login dashboard.
//
// Delapan jam: cukup untuk satu hari kerja tanpa login ulang, dan cukup pendek
// sehingga laptop yang ditinggal terbuka tidak menyimpan akses semalaman.
const SessionLifetime = 8 * time.Hour

// SessionToken adalah sesi yang baru dibuat. Secret hanya ada di sini, sekali.
type SessionToken struct {
	SessionID string
	Hash      []byte
	Secret    string
}

// GenerateSession menerbitkan token sesi baru.
func GenerateSession() (SessionToken, error) {
	id, err := randomString(sessionIDBytes)
	if err != nil {
		return SessionToken{}, err
	}
	secret, err := randomString(sessionSecretLen)
	if err != nil {
		return SessionToken{}, err
	}

	full := strings.Join([]string{sessionPrefix, id, secret}, "_")
	return SessionToken{SessionID: id, Hash: HashToken(full), Secret: full}, nil
}

// ParseSessionID membaca sessionId dari token tanpa memverifikasi apa pun.
func ParseSessionID(token string) (string, error) {
	parts := strings.Split(token, "_")
	if len(parts) != 3 || parts[0] != sessionPrefix || parts[1] == "" || parts[2] == "" {
		return "", ErrMalformedToken
	}
	return parts[1], nil
}

// Session adalah sesi login yang tersimpan.
type Session struct {
	SessionID  string
	Hash       []byte
	UserID     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	LastSeenAt *time.Time
	UserAgent  string
}

// Usable melaporkan apakah sesi masih berlaku pada waktu now.
func (s Session) Usable(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// User adalah operator dashboard.
type User struct {
	UserID       string
	Email        string
	PasswordHash string
	Name         string
	CreatedAt    time.Time
	DisabledAt   *time.Time
	LastLoginAt  *time.Time
}

// Active melaporkan apakah user masih boleh login.
func (u User) Active() bool { return u.DisabledAt == nil }

// SessionScopes adalah izin yang dibawa sesi login dashboard.
//
// Sesi login memegang SELURUH scope, termasuk keys:admin (mencetak kunci API
// baru) dan actor:assert (bertindak atas nama pemain mana pun lewat
// X-User-Id). Itu keputusan sadar: dashboard dipakai tim internal yang memang
// mengurus semuanya.
//
// Konsekuensinya sesi browser yang dibajak setara akses penuh. Kalau suatu saat
// ingin dipersempit, di sinilah tempatnya — cukup buang scope dari daftar ini
// dan seluruh rute ikut menyesuaikan, karena penegakannya ada di router.
var SessionScopes = AllScopes
