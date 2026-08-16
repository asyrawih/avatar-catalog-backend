package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// DashboardAuth adalah login operator dashboard: kata sandi ditukar sesi, dan
// sesi itu yang dibawa tiap request berikutnya.
//
// Jalur ini berdiri di samping kunci API, bukan menggantikannya. Konsumen mesin
// (game server, pengambil data AI) tetap memakai kunci: mereka tidak punya
// tempat mengetik kata sandi dan tidak perlu sesi yang kedaluwarsa tiap delapan
// jam.
type DashboardAuth struct {
	users store.DashboardUsers
	now   func() time.Time
}

// NewDashboardAuth merangkai service login dashboard.
func NewDashboardAuth(users store.DashboardUsers) *DashboardAuth {
	return &DashboardAuth{users: users, now: func() time.Time { return time.Now().UTC() }}
}

// errBadCredentials adalah satu-satunya balasan untuk email tidak ada, kata
// sandi salah, maupun akun dinonaktifkan.
//
// Alasannya sama dengan errUnauthenticated di lapisan HTTP: membedakan "email
// tidak terdaftar" dari "kata sandi salah" mengubah halaman login menjadi alat
// untuk memastikan email siapa saja yang punya akses.
func errBadCredentials() error {
	return apierr.Unauthorized("invalid_credentials", "Email atau kata sandi salah")
}

// LoginResult adalah sesi baru beserta pemiliknya.
type LoginResult struct {
	User    auth.User
	Session auth.Session
	// Token hanya ada di sini, sekali. Yang tersimpan cuma hash-nya.
	Token     string
	ExpiresAt time.Time
}

// Login menukar email dan kata sandi dengan sesi baru.
func (s *DashboardAuth) Login(ctx context.Context, email, password, userAgent string) (LoginResult, error) {
	user, err := s.users.ByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		// Kata sandi tetap dihash walau user-nya tidak ada, supaya lama
		// balasan tidak membedakan email terdaftar dari yang tidak. Tanpa ini,
		// login gagal untuk email yang ada butuh satu argon2 penuh sementara
		// email asing dijawab seketika — dan selisih itu cukup untuk
		// mengumpulkan daftar email operator.
		_, _ = auth.HashPassword(strings.Repeat("x", auth.MinPasswordLen))
		return LoginResult{}, errBadCredentials()
	}
	if err != nil {
		return LoginResult{}, err
	}

	ok, err := auth.VerifyPassword(password, user.PasswordHash)
	if err != nil {
		// Hash rusak adalah kerusakan data, bukan kata sandi salah. Klien tetap
		// menerima jawaban yang sama; yang berbeda hanya jejaknya di log.
		return LoginResult{}, err
	}
	if !ok || !user.Active() {
		return LoginResult{}, errBadCredentials()
	}

	token, err := auth.GenerateSession()
	if err != nil {
		return LoginResult{}, err
	}

	now := s.now()
	session := auth.Session{
		SessionID: token.SessionID,
		Hash:      token.Hash,
		UserID:    user.UserID,
		CreatedAt: now,
		ExpiresAt: now.Add(auth.SessionLifetime),
		UserAgent: truncate(userAgent, 200),
	}
	if err := s.users.CreateSession(ctx, session); err != nil {
		return LoginResult{}, err
	}
	// Catatan operasional; kegagalannya tidak boleh menggagalkan login yang
	// kredensialnya benar.
	_ = s.users.TouchLastLogin(ctx, user.UserID, now)

	user.PasswordHash = ""
	return LoginResult{User: user, Session: session, Token: token.Secret, ExpiresAt: session.ExpiresAt}, nil
}

// Authenticate memverifikasi token sesi dan mengembalikan pemiliknya.
//
// Mengembalikan errBadCredentials untuk semua kegagalan — sesi tidak ada,
// hash tidak cocok, sesi dicabut atau kedaluwarsa, user dinonaktifkan.
func (s *DashboardAuth) Authenticate(ctx context.Context, token string) (auth.User, auth.Session, error) {
	sessionID, err := auth.ParseSessionID(token)
	if err != nil {
		return auth.User{}, auth.Session{}, errBadCredentials()
	}

	session, err := s.users.SessionByID(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return auth.User{}, auth.Session{}, errBadCredentials()
	}
	if err != nil {
		return auth.User{}, auth.Session{}, err
	}

	// Hash dicocokkan sebelum status diperiksa, alasannya sama dengan kunci
	// API: memeriksa status duluan membuat lama balasan membedakan sessionId
	// yang ada dari yang tidak.
	if !auth.Equal(session.Hash, auth.HashToken(token)) {
		return auth.User{}, auth.Session{}, errBadCredentials()
	}
	if !session.Usable(s.now()) {
		return auth.User{}, auth.Session{}, errBadCredentials()
	}

	user, err := s.users.ByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return auth.User{}, auth.Session{}, errBadCredentials()
		}
		return auth.User{}, auth.Session{}, err
	}
	// Menonaktifkan operator harus langsung berlaku pada sesi yang sedang
	// berjalan, bukan menunggu sesinya kedaluwarsa sendiri.
	if !user.Active() {
		return auth.User{}, auth.Session{}, errBadCredentials()
	}

	_ = s.users.TouchSession(ctx, sessionID, s.now())

	user.PasswordHash = ""
	return user, session, nil
}

// Logout mematikan satu sesi. Idempoten: sesi yang sudah mati atau tidak
// dikenal tetap dijawab tanpa error — hasil akhir yang diminta pemanggil
// ("sesi ini tidak berlaku lagi") sudah tercapai.
func (s *DashboardAuth) Logout(ctx context.Context, token string) error {
	sessionID, err := auth.ParseSessionID(token)
	if err != nil {
		return nil
	}
	if err := s.users.RevokeSession(ctx, sessionID, s.now()); err != nil &&
		!errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

// CreateUserInput adalah muatan pembuatan operator baru.
type CreateUserInput struct {
	Email    string
	Name     string
	Password string
}

// CreateUser menambahkan operator dashboard.
func (s *DashboardAuth) CreateUser(ctx context.Context, in CreateUserInput) (auth.User, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if email == "" || !strings.Contains(email, "@") {
		return auth.User{}, apierr.Unprocessable("invalid_email", "Email tidak valid")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = email
	}

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return auth.User{}, apierr.Unprocessable("weak_password", err.Error())
	}

	user := auth.User{
		UserID:       "usr_" + randomHex(6),
		Email:        email,
		PasswordHash: hash,
		Name:         name,
		CreatedAt:    s.now(),
	}
	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return auth.User{}, apierr.Conflict("email_taken", "Email sudah dipakai operator lain")
		}
		return auth.User{}, err
	}

	user.PasswordHash = ""
	return user, nil
}

// ListUsers mengembalikan seluruh operator tanpa hash kata sandinya.
func (s *DashboardAuth) ListUsers(ctx context.Context) ([]auth.User, error) {
	rows, err := s.users.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].PasswordHash = ""
	}
	return rows, nil
}

// SetDisabled menonaktifkan atau mengaktifkan kembali operator.
//
// Menonaktifkan sekaligus mematikan seluruh sesinya: akses yang dicabut harus
// berhenti sekarang, bukan saat sesi terakhirnya kedaluwarsa.
func (s *DashboardAuth) SetDisabled(ctx context.Context, userID string, disabled bool) error {
	var at *time.Time
	if disabled {
		now := s.now()
		at = &now
	}

	if err := s.users.SetDisabled(ctx, userID, at); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NotFound("user_not_found", "Operator tidak ditemukan")
		}
		return err
	}
	if disabled {
		return s.users.RevokeUserSessions(ctx, userID, s.now())
	}
	return nil
}

// UpdateProfileInput adalah muatan perubahan identitas operator. Field nil =
// tidak diubah.
type UpdateProfileInput struct {
	Email *string
	Name  *string
}

// UpdateProfile mengganti email dan/atau nama operator.
//
// Mengganti email TIDAK memutus sesi yang sedang berjalan: sesi terikat pada
// userId, bukan email, dan orang yang sama tetap orang yang sama setelah alamat
// suratnya berubah. Yang memutus sesi adalah ganti kata sandi dan penonaktifan.
func (s *DashboardAuth) UpdateProfile(ctx context.Context, userID string, in UpdateProfileInput) (auth.User, error) {
	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return auth.User{}, apierr.NotFound("user_not_found", "Operator tidak ditemukan")
		}
		return auth.User{}, err
	}

	email := user.Email
	if in.Email != nil {
		email = strings.ToLower(strings.TrimSpace(*in.Email))
		if email == "" || !strings.Contains(email, "@") {
			return auth.User{}, apierr.Unprocessable("invalid_email", "Email tidak valid")
		}
	}
	name := user.Name
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if name == "" {
			name = email
		}
	}

	if err := s.users.UpdateProfile(ctx, userID, email, name); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			return auth.User{}, apierr.Conflict("email_taken", "Email sudah dipakai operator lain")
		case errors.Is(err, store.ErrNotFound):
			return auth.User{}, apierr.NotFound("user_not_found", "Operator tidak ditemukan")
		}
		return auth.User{}, err
	}

	user.Email, user.Name, user.PasswordHash = email, name, ""
	return user, nil
}

// DeleteUser menghapus operator beserta seluruh sesinya.
//
// selfID adalah operator yang sedang memanggil: menghapus diri sendiri ditolak.
// Bukan karena berbahaya bagi data, melainkan karena hasilnya membingungkan —
// sesi pemanggil mati di tengah request yang ia kira berhasil, dan kalau ia
// operator terakhir, tidak ada lagi yang bisa masuk untuk membuat penggantinya.
func (s *DashboardAuth) DeleteUser(ctx context.Context, userID, selfID string) error {
	if userID == selfID {
		return apierr.Conflict("cannot_delete_self",
			"Tidak bisa menghapus akun sendiri; minta operator lain melakukannya")
	}

	if err := s.users.Delete(ctx, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NotFound("user_not_found", "Operator tidak ditemukan")
		}
		return err
	}
	return nil
}

// ChangePassword mengganti kata sandi operator.
//
// Seluruh sesi lamanya ikut dimatikan — mengganti kata sandi adalah yang
// dilakukan orang ketika curiga akunnya dipakai orang lain, dan itu tidak ada
// gunanya kalau sesi si penyusup tetap hidup.
func (s *DashboardAuth) ChangePassword(ctx context.Context, userID, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return apierr.Unprocessable("weak_password", err.Error())
	}
	if err := s.users.SetPassword(ctx, userID, hash); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NotFound("user_not_found", "Operator tidak ditemukan")
		}
		return err
	}
	return s.users.RevokeUserSessions(ctx, userID, s.now())
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
