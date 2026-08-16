package httpapi_test

import (
	"net/http"
	"testing"
	"time"
)

// createEvent menjadwalkan satu event dan mengembalikan balasannya.
func createEvent(t *testing.T, fx *loginFixture, session map[string]string, name string, startsAt, endsAt time.Time) map[string]any {
	t.Helper()

	resp, body := do(t, fx.srv, request{
		method:  http.MethodPost,
		path:    "/v1/cashback/events",
		headers: session,
		body: map[string]any{
			"name":     name,
			"startsAt": startsAt.UTC().Format(time.RFC3339),
			"endsAt":   endsAt.UTC().Format(time.RFC3339),
		},
	})
	requireStatus(t, resp, body, http.StatusCreated)
	return body
}

func TestEventBelumMulaiBisaDiubahDanDihapus(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	now := time.Now().UTC()
	ev := createEvent(t, fx, session, "akhir-pekan", now.Add(24*time.Hour), now.Add(48*time.Hour))
	if ev["phase"] != "upcoming" {
		t.Fatalf("phase = %v, ingin upcoming", ev["phase"])
	}
	if ev["editable"] != true || ev["deletable"] != true {
		t.Errorf("event mendatang seharusnya editable dan deletable: %v", ev)
	}

	eventID := ev["eventId"].(string)
	resp, body := do(t, fx.srv, request{
		method:  http.MethodPatch,
		path:    "/v1/cashback/events/" + eventID,
		headers: session,
		body: map[string]any{
			"name":     "akhir-pekan-panjang",
			"startsAt": now.Add(36 * time.Hour).UTC().Format(time.RFC3339),
		},
	})
	requireStatus(t, resp, body, http.StatusOK)
	if body["name"] != "akhir-pekan-panjang" {
		t.Errorf("name = %v, ingin akhir-pekan-panjang", body["name"])
	}

	resp, body = do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/cashback/events/" + eventID, headers: session,
	})
	requireStatus(t, resp, body, http.StatusNoContent)

	_, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/cashback/events", headers: session})
	for _, row := range body["data"].([]any) {
		if row.(map[string]any)["eventId"] == eventID {
			t.Fatal("event masih ada setelah dihapus")
		}
	}
}

// Event yang sedang berjalan hanya boleh dimajukan waktu selesainya. Menggeser
// nama atau waktu mulainya berarti menulis ulang periode yang rate-nya sudah
// terlanjur diberikan ke pemain.
func TestEventBerjalanHanyaBolehUbahEndsAt(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	now := time.Now().UTC()
	ev := createEvent(t, fx, session, "sedang-jalan", now.Add(-time.Hour), now.Add(time.Hour))
	if ev["phase"] != "running" {
		t.Fatalf("phase = %v, ingin running", ev["phase"])
	}
	if ev["deletable"] != false {
		t.Error("event berjalan seharusnya tidak deletable")
	}
	eventID := ev["eventId"].(string)

	for name, payload := range map[string]map[string]any{
		"ganti nama":        {"name": "nama-baru"},
		"geser waktu mulai": {"startsAt": now.Add(-2 * time.Hour).Format(time.RFC3339)},
	} {
		resp, body := do(t, fx.srv, request{
			method: http.MethodPatch, path: "/v1/cashback/events/" + eventID,
			headers: session, body: payload,
		})
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("%s: status = %d, ingin 409 (body: %v)", name, resp.StatusCode, body)
			continue
		}
		requireErrorCode(t, body, "event_running")
	}

	// Menghentikannya lebih cepat boleh.
	resp, body := do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/cashback/events/" + eventID, headers: session,
		body: map[string]any{"endsAt": now.Add(5 * time.Minute).Format(time.RFC3339)},
	})
	requireStatus(t, resp, body, http.StatusOK)

	// Menghapusnya tidak.
	resp, body = do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/cashback/events/" + eventID, headers: session,
	})
	requireStatus(t, resp, body, http.StatusConflict)
	requireErrorCode(t, body, "event_started")
}

// Menjadwalkan jendela di masa lalu TIDAK dilarang — backfill dan penyiapan
// data uji memakainya. Yang dijaga hanya perubahan sesudahnya: begitu jendela
// itu lewat, barisnya terkunci.
func TestEventLampauLangsungTerkunci(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	now := time.Now().UTC()
	ev := createEvent(t, fx, session, "kemarin", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if ev["phase"] != "finished" {
		t.Fatalf("phase = %v, ingin finished", ev["phase"])
	}
	if ev["editable"] != false || ev["deletable"] != false {
		t.Errorf("event selesai seharusnya terkunci: %v", ev)
	}

	eventID := ev["eventId"].(string)
	resp, body := do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/cashback/events/" + eventID, headers: session,
		body: map[string]any{"name": "diubah"},
	})
	requireStatus(t, resp, body, http.StatusConflict)
	requireErrorCode(t, body, "event_finished")

	resp, body = do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/cashback/events/" + eventID, headers: session,
	})
	requireStatus(t, resp, body, http.StatusConflict)
	requireErrorCode(t, body, "event_started")
}

func TestEventMendatangTidakBisaDigeserKeMasaLalu(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	now := time.Now().UTC()
	ev := createEvent(t, fx, session, "besok", now.Add(24*time.Hour), now.Add(48*time.Hour))

	resp, body := do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/cashback/events/" + ev["eventId"].(string),
		headers: session,
		body:    map[string]any{"startsAt": now.Add(-time.Hour).Format(time.RFC3339)},
	})
	requireStatus(t, resp, body, http.StatusUnprocessableEntity)
	requireErrorCode(t, body, "window_in_past")
}

func TestUbahEventYangTidakAda(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	resp, body := do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/cashback/events/evt_tidakada", headers: session,
		body: map[string]any{"name": "apa saja"},
	})
	requireStatus(t, resp, body, http.StatusNotFound)
	requireErrorCode(t, body, "event_not_found")

	resp, body = do(t, fx.srv, request{
		method: http.MethodDelete, path: "/v1/cashback/events/evt_tidakada", headers: session,
	})
	requireStatus(t, resp, body, http.StatusNotFound)
	requireErrorCode(t, body, "event_not_found")
}

// Menghentikan event berjalan lewat endsAt=sekarang versi klien selalu kalah
// cepat: input datetime-local hanya sampai menit, dan perjalanan request sudah
// cukup membuat nilainya jatuh di masa lalu. active=false memakai jam server,
// jadi tombol "hentikan" tidak gagal karena alasan sehalus itu.
func TestHentikanEventBerjalanLewatActiveFalse(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	now := time.Now().UTC()
	ev := createEvent(t, fx, session, "sedang-jalan", now.Add(-time.Hour), now.Add(time.Hour))
	eventID := ev["eventId"].(string)

	// Jalur lama memang gagal: "sekarang" dibulatkan ke menit sudah lewat.
	resp, body := do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/cashback/events/" + eventID, headers: session,
		body: map[string]any{"endsAt": now.Truncate(time.Minute).Format(time.RFC3339)},
	})
	requireStatus(t, resp, body, http.StatusUnprocessableEntity)
	requireErrorCode(t, body, "window_in_past")

	// Jalur barunya berhasil, dan eventnya langsung tidak aktif lagi.
	resp, body = do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/cashback/events/" + eventID, headers: session,
		body: map[string]any{"active": false},
	})
	requireStatus(t, resp, body, http.StatusOK)
	if body["active"] != false {
		t.Errorf("active = %v, ingin false", body["active"])
	}
	if body["phase"] != "finished" {
		t.Errorf("phase = %v, ingin finished", body["phase"])
	}

	// Dan bonusnya berhenti berlaku: event itu tidak lagi terhitung aktif.
	_, body = do(t, fx.srv, request{method: http.MethodGet, path: "/v1/cashback/events", headers: session})
	for _, row := range body["data"].([]any) {
		if e := row.(map[string]any); e["eventId"] == eventID && e["active"] == true {
			t.Fatal("event masih aktif setelah dihentikan")
		}
	}
}

func TestActiveFalseHanyaUntukEventBerjalan(t *testing.T) {
	fx := newLoginServer(t)
	session := fx.login(t, fx.email, testPassword)

	now := time.Now().UTC()
	upcoming := createEvent(t, fx, session, "besok", now.Add(24*time.Hour), now.Add(48*time.Hour))
	resp, body := do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/cashback/events/" + upcoming["eventId"].(string),
		headers: session, body: map[string]any{"active": false},
	})
	requireStatus(t, resp, body, http.StatusConflict)
	requireErrorCode(t, body, "event_not_started")

	// Mengaktifkan ulang tidak didukung: itu menulis ulang periode yang rate-nya
	// sudah terlanjur dihitung tanpa bonus.
	running := createEvent(t, fx, session, "jalan", now.Add(-time.Hour), now.Add(time.Hour))
	resp, body = do(t, fx.srv, request{
		method: http.MethodPatch, path: "/v1/cashback/events/" + running["eventId"].(string),
		headers: session, body: map[string]any{"active": true},
	})
	requireStatus(t, resp, body, http.StatusUnprocessableEntity)
	requireErrorCode(t, body, "cannot_reactivate")
}
