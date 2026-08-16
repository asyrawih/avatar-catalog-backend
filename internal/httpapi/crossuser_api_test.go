package httpapi_test

import (
	"net/http"
	"testing"
)

// seedTx mencatat satu transaksi sukses untuk userID lalu mengembalikan txId-nya.
func seedTx(t *testing.T, fx *loginFixture, session map[string]string, userID int64, key string, price int) string {
	t.Helper()

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/transactions",
		headers: withHeader(session, "Idempotency-Key", key),
		body: map[string]any{
			"userId": userID, "universeId": 1, "placeId": 2, "jobId": "job-" + key,
			"status": "success", "occurredAt": "2026-08-16T10:00:00Z",
			"items": []map[string]any{{"assetId": 123, "price": price, "result": "success"}},
		},
	})
	requireStatus(t, resp, body, http.StatusCreated)
	return body["txId"].(string)
}

func withHeader(base map[string]string, name, value string) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out[name] = value
	return out
}

// Dashboard memantau arus transaksi apa adanya, bukan riwayat satu pemain,
// jadi userId sekarang opsional — dan tiap baris membawa userId-nya sendiri
// supaya daftar lintas pemain bisa dibaca.
func TestDaftarTransaksiTanpaUserIdMemuatSemuaPemain(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	seedTx(t, fx, session, 1001, "a", 500)
	seedTx(t, fx, session, 1002, "b", 300)

	resp, body := do(t, fx.srv, request{method: http.MethodGet, path: "/v1/transactions", headers: session})
	requireStatus(t, resp, body, http.StatusOK)

	rows := body["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("jumlah transaksi = %d, ingin 2 (body: %v)", len(rows), body)
	}

	seen := map[float64]bool{}
	for _, row := range rows {
		tx := row.(map[string]any)
		userID, ok := tx["userId"].(float64)
		if !ok {
			t.Fatalf("baris transaksi tanpa userId: %v", tx)
		}
		seen[userID] = true
	}
	if !seen[1001] || !seen[1002] {
		t.Errorf("daftar tidak memuat kedua pemain: %v", seen)
	}

	// Dengan userId, daftarnya kembali menyempit ke satu pemain.
	resp, body = do(t, fx.srv, request{
		method: http.MethodGet, path: "/v1/transactions?userId=1001", headers: session,
	})
	requireStatus(t, resp, body, http.StatusOK)
	if rows := body["data"].([]any); len(rows) != 1 {
		t.Errorf("jumlah transaksi userId=1001 = %d, ingin 1", len(rows))
	}
}

func TestDaftarSaldoCashbackLintasPemain(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	seedTx(t, fx, session, 2001, "c", 1000)
	seedTx(t, fx, session, 2002, "d", 500)

	resp, body := do(t, fx.srv, request{
		method: http.MethodGet, path: "/v1/cashback/balances", headers: session,
	})
	requireStatus(t, resp, body, http.StatusOK)

	rows := body["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("jumlah baris saldo = %d, ingin 2 (body: %v)", len(rows), body)
	}

	byUser := map[float64]map[string]any{}
	for _, row := range rows {
		entry := row.(map[string]any)
		byUser[entry["userId"].(float64)] = entry
	}

	// Belanja 1000 dengan rate mana pun harus menghasilkan saldo positif, dan
	// akrualnya sama dengan saldo selama belum ada pencairan.
	first := byUser[2001]
	if first == nil {
		t.Fatalf("pemain 2001 tidak ada di daftar: %v", byUser)
	}
	if first["balance"].(float64) <= 0 {
		t.Errorf("saldo 2001 = %v, ingin > 0", first["balance"])
	}
	if first["balance"] != first["accrued"] {
		t.Errorf("saldo (%v) dan akrual (%v) berbeda padahal belum ada pencairan",
			first["balance"], first["accrued"])
	}
	if first["entryCount"].(float64) != 1 {
		t.Errorf("entryCount 2001 = %v, ingin 1", first["entryCount"])
	}

	// Belanja lebih besar menghasilkan saldo lebih besar.
	if byUser[2001]["balance"].(float64) <= byUser[2002]["balance"].(float64) {
		t.Errorf("saldo 2001 (%v) tidak lebih besar dari 2002 (%v)",
			byUser[2001]["balance"], byUser[2002]["balance"])
	}
}

// Pencairan harus terlihat di daftar: saldo turun, sementara akrual — yang
// mencatat sejarah, bukan sisa — tidak ikut berkurang.
func TestDaftarSaldoMemisahkanAkrualDariPencairan(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	seedTx(t, fx, session, 3001, "e", 2000)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/cashback/redeems",
		headers: session,
		body:    map[string]any{"userId": 3001},
	})
	requireStatus(t, resp, body, http.StatusCreated)
	redeemed := body["amount"].(float64)

	_, body = do(t, fx.srv, request{
		method: http.MethodGet, path: "/v1/cashback/balances", headers: session,
	})
	row := body["data"].([]any)[0].(map[string]any)

	if row["balance"].(float64) != 0 {
		t.Errorf("saldo sesudah redeem = %v, ingin 0", row["balance"])
	}
	if row["redeemed"].(float64) != redeemed {
		t.Errorf("redeemed = %v, ingin %v", row["redeemed"], redeemed)
	}
	if row["accrued"].(float64) != redeemed {
		t.Errorf("accrued = %v, ingin tetap %v setelah pencairan", row["accrued"], redeemed)
	}
}
