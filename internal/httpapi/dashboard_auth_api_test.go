package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/httpapi"
	"github.com/hanan/avatar-catalog-backend/internal/idempotency"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

const testPassword = "kata-sandi-yang-panjang"

// loginFixture adalah server dengan login dashboard aktif di samping kunci API.
type loginFixture struct {
	srv   *httptest.Server
	users *store.MemoryDashboardUsers
	email string
}

func newLoginServer(t *testing.T) *loginFixture {
	t.Helper()

	outfits := store.NewMemoryOutfits()
	templates := store.NewMemoryTemplates()
	transactions := store.NewMemoryTransactions()
	store.SeedData(templates, outfits)

	keys := store.NewMemoryAPIKeys()
	users := store.NewMemoryDashboardUsers()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dashboard := service.NewDashboardAuth(users)
	if _, err := dashboard.CreateUser(context.Background(), service.CreateUserInput{
		Email: "ops@contoh.com", Name: "Tim Ops", Password: testPassword,
	}); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	cashback := service.NewCashback(store.NewMemoryCashback(transactions))
	// CookieSecure false: httptest melayani http://, dan cookie Secure tidak
	// akan pernah dikirim balik oleh klien di atasnya.
	handler := httpapi.NewRouter(httpapi.Deps{
		Outfits:       service.NewOutfits(outfits, templates),
		Transactions:  service.NewTransactions(transactions, cashback),
		Cashback:      cashback,
		Templates:     service.NewTemplates(templates),
		APIKeys:       service.NewAPIKeys(keys),
		DashboardAuth: dashboard,
		CookieSecure:  false,
		Idempotency:   idempotency.NewMemoryStore(time.Hour),
		Auth: httpapi.NewSessionAuth(dashboard, httpapi.NewKeyAuth(keys, logger),
			httpapi.NewCookieConfig(false), logger),
		Logger: logger,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &loginFixture{srv: srv, users: users, email: "ops@contoh.com"}
}

// login menukar kredensial dengan cookie sesi, lalu mengembalikan cookie itu
// sebagai header siap pakai.
func (f *loginFixture) login(t *testing.T, email, password string) map[string]string {
	t.Helper()

	resp, body := do(t, f.srv, request{
		method: http.MethodPost,
		path:   "/v1/auth/login",
		body:   map[string]any{"email": email, "password": password},
	})
	requireStatus(t, resp, body, http.StatusOK)

	for _, cookie := range resp.Cookies() {
		if cookie.Name == "acb_session" {
			return map[string]string{"Cookie": cookie.Name + "=" + cookie.Value}
		}
	}
	t.Fatal("balasan login tidak memuat cookie sesi")
	return nil
}

// Token sesi TIDAK boleh ikut di body: token yang bisa dibaca JavaScript
// halaman membuat satu XSS cukup untuk mencurinya, dan itulah yang justru
// dihindari dengan memakai cookie httpOnly.
func TestLoginMengirimSesiHanyaLewatCookieHttpOnly(t *testing.T) {
	fx := newLoginServer(t)

	resp, body := do(t, fx.srv, request{
		method: http.MethodPost,
		path:   "/v1/auth/login",
		body:   map[string]any{"email": fx.email, "password": testPassword},
	})
	requireStatus(t, resp, body, http.StatusOK)

	if _, ada := body["token"]; ada {
		t.Error("body login memuat token sesi")
	}
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("balasan tidak memuat user: %v", body)
	}
	if _, ada := user["passwordHash"]; ada {
		t.Error("balasan login membocorkan hash kata sandi")
	}

	var found bool
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "acb_session" {
			continue
		}
		found = true
		if !cookie.HttpOnly {
			t.Error("cookie sesi tidak HttpOnly")
		}
		if cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie sesi SameSite = %v, ingin Lax", cookie.SameSite)
		}
		if cookie.Path != "/" {
			t.Errorf("cookie sesi Path = %q, ingin /", cookie.Path)
		}
	}
	if !found {
		t.Fatal("tidak ada cookie sesi di balasan")
	}
}

// Email tidak terdaftar dan kata sandi salah harus dijawab persis sama.
// Membedakannya mengubah halaman login jadi alat untuk memastikan email siapa
// saja yang punya akses.
func TestLoginGagalMenjawabSama(t *testing.T) {
	fx := newLoginServer(t)

	cases := map[string]map[string]any{
		"email tidak terdaftar": {"email": "bukan@contoh.com", "password": testPassword},
		"kata sandi salah":      {"email": fx.email, "password": "salah-sekali-panjangnya"},
		"kata sandi kosong":     {"email": fx.email, "password": ""},
	}
	for name, payload := range cases {
		resp, body := do(t, fx.srv, request{
			method: http.MethodPost, path: "/v1/auth/login", body: payload,
		})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, ingin 401 (body: %v)", name, resp.StatusCode, body)
			continue
		}
		requireErrorCode(t, body, "invalid_credentials")
	}
}

func TestSesiLoginBisaDipakaiTanpaKunciAPI(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	// Sesi membawa seluruh scope: baca transaksi, tuntaskan redeem, sampai
	// menerbitkan kunci API.
	for _, path := range []string{
		"/v1/transactions?userId=627278822",
		"/v1/cashback/summary?userId=627278822",
		"/v1/cashback/redeems",
		"/v1/keys",
		"/v1/outfits",
	} {
		resp, body := do(t, fx.srv, request{method: http.MethodGet, path: path, headers: session})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, ingin 200 (body: %v)", path, resp.StatusCode, body)
		}
	}
}

func TestMeMelaporkanSesiBerjalanDanBerhentiSetelahLogout(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	resp, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/me", headers: session})
	requireStatus(t, resp, body, http.StatusOK)
	if user := body["user"].(map[string]any); user["email"] != fx.email {
		t.Errorf("me.email = %v, ingin %q", user["email"], fx.email)
	}

	resp, body = do(t, fx.srv, request{method: http.MethodPost, path: "/v1/auth/logout", headers: session})
	requireStatus(t, resp, body, http.StatusNoContent)

	// Cookie lama tidak boleh bisa dipakai lagi — inilah yang membedakan sesi
	// tersimpan dari JWT tanpa state.
	resp, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/me", headers: session})
	requireStatus(t, resp, body, http.StatusUnauthorized)

	resp, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/outfits", headers: session})
	requireStatus(t, resp, body, http.StatusUnauthorized)

	// Logout kedua tetap dijawab bersih: yang diminta pemanggil sudah tercapai.
	resp, body = do(t, fx.srv, request{method: http.MethodPost, path: "/v1/auth/logout", headers: session})
	requireStatus(t, resp, body, http.StatusNoContent)
}

// Menonaktifkan operator harus langsung menghentikan sesi yang sedang berjalan,
// bukan menunggu sesinya kedaluwarsa sendiri delapan jam kemudian.
func TestOperatorDinonaktifkanLangsungKehilanganSesi(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	resp, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/users", headers: session})
	requireStatus(t, resp, body, http.StatusOK)

	rows := body["data"].([]any)
	userID := rows[0].(map[string]any)["userId"].(string)

	resp, body = do(t, fx.srv, request{
		method:  http.MethodPatch,
		path:    "/v1/auth/users/" + userID,
		headers: session,
		body:    map[string]any{"disabled": true},
	})
	requireStatus(t, resp, body, http.StatusNoContent)

	resp, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/outfits", headers: session})
	requireStatus(t, resp, body, http.StatusUnauthorized)

	// ...dan login baru pun ditolak, dengan pesan yang sama seperti kata sandi
	// salah.
	resp, body = do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/auth/login",
		body: map[string]any{"email": fx.email, "password": testPassword},
	})
	requireStatus(t, resp, body, http.StatusUnauthorized)
	requireErrorCode(t, body, "invalid_credentials")
}

func TestGantiKataSandiMematikanSesiLama(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	_, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/users", headers: session})
	userID := body["data"].([]any)[0].(map[string]any)["userId"].(string)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPatch,
		path:    "/v1/auth/users/" + userID,
		headers: session,
		body:    map[string]any{"password": "kata-sandi-baru-yang-panjang"},
	})
	requireStatus(t, resp, body, http.StatusNoContent)

	resp, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/outfits", headers: session})
	requireStatus(t, resp, body, http.StatusUnauthorized)

	// Kata sandi baru berlaku, yang lama tidak.
	fx.login(t, fx.email, "kata-sandi-baru-yang-panjang")
	resp, body = do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/auth/login",
		body: map[string]any{"email": fx.email, "password": testPassword},
	})
	requireStatus(t, resp, body, http.StatusUnauthorized)
}

func TestKataSandiPendekDitolak(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/auth/users",
		headers: session,
		body:    map[string]any{"email": "baru@contoh.com", "password": "pendek"},
	})
	requireStatus(t, resp, body, http.StatusUnprocessableEntity)
	requireErrorCode(t, body, "weak_password")
}

func TestEmailGandaDitolak(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/auth/users",
		headers: session,
		body:    map[string]any{"email": fx.email, "password": "kata-sandi-yang-lain-lagi"},
	})
	requireStatus(t, resp, body, http.StatusConflict)
	requireErrorCode(t, body, "email_taken")
}

// Cookie sesi yang sudah mati tidak boleh menjatuhkan request yang JUGA membawa
// kunci API yang sah — mis. tab lama yang sesinya kedaluwarsa.
func TestKunciAPITetapJalanWalauCookieSesiBasi(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)
	do(t, fx.srv, request{method: http.MethodPost, path: "/v1/auth/logout", headers: session})

	// Terbitkan kunci lewat sesi baru, lalu pakai kunci itu bersama cookie mati.
	fresh := fx.login(t, fx.email, testPassword)
	_, body := do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/keys", headers: fresh,
		body: map[string]any{
			"name": "uji", "role": "dashboard", "expiresInHours": 24, "env": "test",
		},
	})
	token := body["token"].(string)

	headers := map[string]string{
		"Cookie":        session["Cookie"],
		"Authorization": "Bearer " + token,
	}
	resp, body := do(t, fx.srv, request{
		method: http.MethodGet, path: "/v1/transactions?userId=627278822", headers: headers,
	})
	requireStatus(t, resp, body, http.StatusOK)
}

func TestTanpaLoginTetapDitolak(t *testing.T) {
	fx := newLoginServer(t)

	resp, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/me"})
	requireStatus(t, resp, body, http.StatusUnauthorized)
	requireErrorCode(t, body, "no_session")

	resp, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/outfits"})
	requireStatus(t, resp, body, http.StatusUnauthorized)
}

func TestUbahIdentitasOperator(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	_, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/users", headers: session})
	userID := body["data"].([]any)[0].(map[string]any)["userId"].(string)

	resp, body := do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/auth/users/" + userID, headers: session,
		body: map[string]any{"email": "ops-baru@contoh.com", "name": "Tim Ops v2"},
	})
	requireStatus(t, resp, body, http.StatusNoContent)

	// Ganti email TIDAK memutus sesi: sesi terikat userId, bukan alamat surat.
	resp, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/me", headers: session})
	requireStatus(t, resp, body, http.StatusOK)
	if user := body["user"].(map[string]any); user["email"] != "ops-baru@contoh.com" {
		t.Errorf("email = %v, ingin ops-baru@contoh.com", user["email"])
	}

	// Dan login dengan email baru berhasil, kata sandinya tidak berubah.
	fx.login(t, "ops-baru@contoh.com", testPassword)
}

func TestHapusOperator(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	resp, body := do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/auth/users", headers: session,
		body: map[string]any{"email": "sekali-pakai@contoh.com", "password": "kata-sandi-yang-panjang"},
	})
	requireStatus(t, resp, body, http.StatusCreated)
	targetID := body["userId"].(string)

	resp, body = do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/auth/users/" + targetID, headers: session,
	})
	requireStatus(t, resp, body, http.StatusNoContent)

	// Barisnya hilang, dan login dengan akun itu ditolak seperti akun asing.
	_, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/users", headers: session})
	for _, row := range body["data"].([]any) {
		if row.(map[string]any)["userId"] == targetID {
			t.Fatal("operator masih ada setelah dihapus")
		}
	}

	resp, body = do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/auth/login",
		body: map[string]any{"email": "sekali-pakai@contoh.com", "password": "kata-sandi-yang-panjang"},
	})
	requireStatus(t, resp, body, http.StatusUnauthorized)
}

// Menghapus akun sendiri ditolak: sesi pemanggil mati di tengah request yang ia
// kira berhasil, dan kalau ia operator terakhir, tidak ada lagi yang bisa masuk
// untuk membuat penggantinya.
func TestOperatorTidakBisaMenghapusDirinyaSendiri(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	_, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/users", headers: session})
	selfID := body["data"].([]any)[0].(map[string]any)["userId"].(string)

	resp, body := do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/auth/users/" + selfID, headers: session,
	})
	requireStatus(t, resp, body, http.StatusConflict)
	requireErrorCode(t, body, "cannot_delete_self")

	// Sesinya tetap hidup.
	resp, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/auth/me", headers: session})
	requireStatus(t, resp, body, http.StatusOK)
}

func TestEmailOperatorTidakBolehBentrokSaatDiubah(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	resp, body := do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/auth/users", headers: session,
		body: map[string]any{"email": "kedua@contoh.com", "password": "kata-sandi-yang-panjang"},
	})
	requireStatus(t, resp, body, http.StatusCreated)
	keduaID := body["userId"].(string)

	resp, body = do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/auth/users/" + keduaID, headers: session,
		body: map[string]any{"email": fx.email},
	})
	requireStatus(t, resp, body, http.StatusConflict)
	requireErrorCode(t, body, "email_taken")
}
