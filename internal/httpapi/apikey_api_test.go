package httpapi_test

import (
	"net/http"
	"testing"
)

// keysAdmin adalah token dengan scope keys:admin, dari role keys-admin.
func keysAdmin(fx *authFixture) map[string]string { return bearer(fx.tokens["keys-admin"]) }

// Kunci yang bisa mencetak kunci lain adalah eskalasi paling berbahaya di API
// ini. Yang menahannya cuma satu hal: keys:admin tidak ikut di role mana pun
// selain keys-admin. Test ini yang menjaga janji itu tidak diam-diam hilang
// saat suatu hari ada yang menambah scope ke role dashboard karena praktis.
func TestKeysAdminTidakAdaDiRoleLain(t *testing.T) {
	fx := newAuthServer(t)

	for _, role := range []string{"dashboard", "game-server", "ai", "public-read"} {
		resp, body := do(t, fx.srv, request{
			method:  http.MethodGet,
			path:    "/v1/keys",
			headers: bearer(fx.tokens[role]),
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, ingin 403 (body: %v)", role, resp.StatusCode, body)
		}
	}
}

func TestTerbitkanKunciMengembalikanTokenSekaliDanLangsungBisaDipakai(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/keys",
		headers: keysAdmin(fx),
		body: map[string]any{
			"name":           "dashboard-sementara",
			"role":           "dashboard",
			"expiresInHours": 24,
			"env":            "test",
		},
	})
	requireStatus(t, resp, body, http.StatusCreated)

	token, ok := body["token"].(string)
	if !ok || token == "" {
		t.Fatalf("balasan tidak memuat token: %v", body)
	}
	if body["status"] != "active" {
		t.Errorf("status = %v, ingin active", body["status"])
	}
	if body["expiresAt"] == nil {
		t.Error("expiresAt kosong; jalur HTTP tidak boleh menerbitkan kunci abadi")
	}

	// Kunci baru harus benar-benar berlaku, dengan scope role-nya.
	resp, body = do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/transactions?userId=627278822",
		headers: bearer(token),
	})
	requireStatus(t, resp, body, http.StatusOK)

	// ...dan tidak lebih dari itu: dashboard tidak memegang catalog:write.
	resp, body = do(t, fx.srv, request{
		method:  http.MethodDelete,
		path:    "/v1/outfits/otf_9f2a41",
		headers: bearer(token),
	})
	requireStatus(t, resp, body, http.StatusForbidden)

	// Token tidak boleh bisa dibaca ulang lewat daftar.
	resp, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/keys", headers: keysAdmin(fx)})
	requireStatus(t, resp, body, http.StatusOK)

	rows, ok := body["data"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("daftar kunci kosong: %v", body)
	}
	for _, row := range rows {
		key := row.(map[string]any)
		if _, ada := key["token"]; ada {
			t.Fatalf("daftar kunci membocorkan token: %v", key)
		}
		if _, ada := key["hash"]; ada {
			t.Fatalf("daftar kunci membocorkan hash: %v", key)
		}
	}
}

func TestTerbitkanKunciMenolakMasukanYangTidakJelas(t *testing.T) {
	fx := newAuthServer(t)

	cases := map[string]struct {
		body map[string]any
		code string
	}{
		"tanpa nama": {
			body: map[string]any{"role": "dashboard", "expiresInHours": 24},
			code: "missing_name",
		},
		"tanpa masa berlaku": {
			body: map[string]any{"name": "x", "role": "dashboard"},
			code: "missing_expires_in",
		},
		"masa berlaku kelewat panjang": {
			body: map[string]any{"name": "x", "role": "dashboard", "expiresInHours": 365*24 + 1},
			code: "expires_in_too_long",
		},
		"role dan scopes sekaligus": {
			body: map[string]any{
				"name": "x", "role": "dashboard",
				"scopes": []string{"catalog:read"}, "expiresInHours": 24,
			},
			code: "ambiguous_scopes",
		},
		"role tidak dikenal": {
			body: map[string]any{"name": "x", "role": "supervisor", "expiresInHours": 24},
			code: "unknown_role",
		},
		"scope tidak dikenal": {
			body: map[string]any{"name": "x", "scopes": []string{"catalog:hapus-semua"}, "expiresInHours": 24},
			code: "invalid_scopes",
		},
	}

	for name, tc := range cases {
		resp, body := do(t, fx.srv, request{
			method:  http.MethodPost,
			path:    "/v1/keys",
			headers: keysAdmin(fx),
			body:    tc.body,
		})
		if resp.StatusCode == http.StatusCreated {
			t.Errorf("%s: kunci malah terbit (body: %v)", name, body)
			continue
		}
		requireErrorCode(t, body, tc.code)
	}
}

func TestCabutKunciLewatAPIMenghentikanPemakaiannya(t *testing.T) {
	fx := newAuthServer(t)

	_, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/keys",
		headers: keysAdmin(fx),
		body: map[string]any{
			"name": "umur-pendek", "scopes": []string{"catalog:read"},
			"expiresInHours": 1, "env": "test",
		},
	})
	token := body["token"].(string)
	keyID := body["keyId"].(string)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodDelete,
		path:    "/v1/keys/" + keyID,
		headers: keysAdmin(fx),
	})
	requireStatus(t, resp, body, http.StatusNoContent)

	resp, body = do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/outfits",
		headers: bearer(token),
	})
	requireStatus(t, resp, body, http.StatusUnauthorized)
	requireErrorCode(t, body, "unauthenticated")

	// Mencabut dua kali bukan error: hasil akhirnya sama.
	resp, body = do(t, fx.srv, request{
		method:  http.MethodDelete,
		path:    "/v1/keys/" + keyID,
		headers: keysAdmin(fx),
	})
	requireStatus(t, resp, body, http.StatusNoContent)

	// Baris kuncinya tetap ada — riwayat kunci yang dicabut justru yang
	// dibutuhkan saat menelusuri insiden.
	_, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/keys", headers: keysAdmin(fx)})
	var status string
	for _, row := range body["data"].([]any) {
		key := row.(map[string]any)
		if key["keyId"] == keyID {
			status = key["status"].(string)
		}
	}
	if status != "revoked" {
		t.Errorf("status kunci dicabut = %q, ingin revoked", status)
	}
}

func TestCabutKunciYangTidakAda(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodDelete,
		path:    "/v1/keys/tidakadakunciini",
		headers: keysAdmin(fx),
	})
	requireStatus(t, resp, body, http.StatusNotFound)
	requireErrorCode(t, body, "key_not_found")
}

func TestDaftarRoleUntukPenerbit(t *testing.T) {
	fx := newAuthServer(t)

	resp, body := do(t, fx.srv, request{
		method:  http.MethodGet,
		path:    "/v1/keys/roles",
		headers: keysAdmin(fx),
	})
	requireStatus(t, resp, body, http.StatusOK)

	roles, ok := body["data"].([]any)
	if !ok || len(roles) == 0 {
		t.Fatalf("daftar role kosong: %v", body)
	}
	if body["maxExpiresInHours"] != float64(365*24) {
		t.Errorf("maxExpiresInHours = %v, ingin %d", body["maxExpiresInHours"], 365*24)
	}
	if scopes, ok := body["allScopes"].([]any); !ok || len(scopes) == 0 {
		t.Error("allScopes kosong; UI penerbit tidak punya daftar untuk dipilih")
	}
}

func TestUbahNamaDanMasaBerlakuKunci(t *testing.T) {
	fx := newAuthServer(t)

	_, body := do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/keys", headers: keysAdmin(fx),
		body: map[string]any{
			"name": "sebelum", "role": "ai", "expiresInHours": 24, "env": "test",
		},
	})
	keyID := body["keyId"].(string)
	token := body["token"].(string)
	scopesSebelum := body["scopes"]

	resp, body := do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/keys/" + keyID, headers: keysAdmin(fx),
		body: map[string]any{"name": "sesudah", "expiresInHours": 48},
	})
	requireStatus(t, resp, body, http.StatusOK)
	if body["name"] != "sesudah" {
		t.Errorf("name = %v, ingin sesudah", body["name"])
	}

	// Scope TIDAK ikut berubah, dan rahasianya tetap sama — pemakai kunci tidak
	// perlu memasang ulang apa pun.
	if got, want := body["scopes"], scopesSebelum; !equalScopes(got, want) {
		t.Errorf("scopes berubah: %v, ingin %v", got, want)
	}
	resp, body = do(t, fx.srv, request{
		method: http.MethodGet, path: "/v1/outfits", headers: bearer(token),
	})
	requireStatus(t, resp, body, http.StatusOK)
}

func equalScopes(a, b any) bool {
	left, ok1 := a.([]any)
	right, ok2 := b.([]any)
	if !ok1 || !ok2 || len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// Scope tidak boleh bisa dinaikkan lewat PATCH: kunci yang beredar dengan izin
// yang diam-diam bertambah adalah eskalasi yang tidak terlihat pemakainya.
func TestPatchKunciTidakBisaMenaikkanScope(t *testing.T) {
	fx := newAuthServer(t)

	_, body := do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/keys", headers: keysAdmin(fx),
		body: map[string]any{"name": "ai", "role": "ai", "expiresInHours": 24, "env": "test"},
	})
	keyID := body["keyId"].(string)
	token := body["token"].(string)

	do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/keys/" + keyID, headers: keysAdmin(fx),
		body: map[string]any{"name": "ai", "scopes": []string{"keys:admin"}},
	})

	// Kunci role ai tetap tidak boleh menulis apa pun.
	resp, body := do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/outfits/otf_9f2a41", headers: bearer(token),
	})
	requireStatus(t, resp, body, http.StatusForbidden)
}

// DELETE bawaan mencabut (barisnya tetap ada untuk penelusuran); ?hard=true
// menghapusnya sungguhan.
func TestHapusKunciPermanen(t *testing.T) {
	fx := newAuthServer(t)

	_, body := do(t, fx.srv, request{
		method: http.MethodPost, path: "/v1/keys", headers: keysAdmin(fx),
		body: map[string]any{"name": "salah-buat", "role": "ai", "expiresInHours": 24, "env": "test"},
	})
	keyID := body["keyId"].(string)

	resp, body := do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/keys/" + keyID + "?hard=true", headers: keysAdmin(fx),
	})
	requireStatus(t, resp, body, http.StatusNoContent)

	_, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/keys", headers: keysAdmin(fx)})
	for _, row := range body["data"].([]any) {
		if row.(map[string]any)["keyId"] == keyID {
			t.Fatal("kunci masih ada setelah dihapus permanen")
		}
	}

	resp, body = do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/keys/" + keyID + "?hard=true", headers: keysAdmin(fx),
	})
	requireStatus(t, resp, body, http.StatusNotFound)
}
