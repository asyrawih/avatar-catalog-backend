package httpapi_test

import (
	"net/http"
	"testing"
)

// TestAdjustTerbawaKeSeluruhEndpoint memastikan adjust yang dikirim saat POST
// muncul kembali di GET detail, GET daftar, dan POST resolve — ketiganya
// membangun item lewat jalur DTO yang sama, jadi satu yang bocor berarti
// klien merender avatar dengan penempatan yang salah.
func TestAdjustTerbawaKeSeluruhEndpoint(t *testing.T) {
	srv := newTestServer(t)

	adjust := map[string]any{
		"pos":   map[string]any{"x": 0, "y": -0.3, "z": 0},
		"rot":   nil,
		"scale": nil,
	}
	create := createOutfitBody()
	create["items"] = []map[string]any{
		{"assetId": assetHair, "slot": "Hair", "adjust": adjust},
		{"assetId": assetJacket, "slot": "Jacket"},
	}

	// POST hanya mengembalikan tanda terima (outfitId/referenceId), bukan
	// detail — item-nya diperiksa lewat pembacaan di bawah.
	resp, created := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: create})
	requireStatus(t, resp, created, http.StatusCreated)

	outfitID, _ := created["outfitId"].(string)
	referenceID, _ := created["referenceId"].(string)

	resp, detail := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + outfitID})
	requireStatus(t, resp, detail, http.StatusOK)
	requireAdjust(t, detail, "GET /v1/outfits/{outfitId}")

	resp, list := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?userId=627278822"})
	requireStatus(t, resp, list, http.StatusOK)
	requireAdjustInList(t, list, outfitID, "GET /v1/outfits")

	resp, resolved := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits/resolve",
		body:   map[string]any{"referenceIds": []string{referenceID}},
	})
	requireStatus(t, resp, resolved, http.StatusOK)
	requireAdjustInList(t, resolved, outfitID, "POST /v1/outfits/resolve")
}

// requireAdjustInList mencari outfit yang baru dibuat di dalam field data lalu
// memeriksa item-nya.
func requireAdjustInList(t *testing.T, body map[string]any, outfitID, endpoint string) {
	t.Helper()

	data, _ := body["data"].([]any)
	for _, entry := range data {
		outfit, _ := entry.(map[string]any)
		if outfit["outfitId"] == outfitID {
			requireAdjust(t, outfit, endpoint)
			return
		}
	}
	t.Fatalf("%s: outfit %s tidak ada di data", endpoint, outfitID)
}

// requireAdjust memeriksa item Hair membawa adjust.pos yang dikirim, dan item
// Jacket yang tidak melaporkannya tidak dibuatkan adjust karangan.
func requireAdjust(t *testing.T, outfit map[string]any, endpoint string) {
	t.Helper()

	items, _ := outfit["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("%s: items kosong", endpoint)
	}

	seen := false
	for _, entry := range items {
		item, _ := entry.(map[string]any)
		if item["slot"] != "Hair" {
			if _, ada := item["adjust"]; ada {
				t.Errorf("%s: item slot %v punya adjust, ingin tidak ada", endpoint, item["slot"])
			}
			continue
		}
		seen = true

		got, ok := item["adjust"].(map[string]any)
		if !ok {
			t.Fatalf("%s: item Hair tanpa adjust (item: %v)", endpoint, item)
		}
		pos, ok := got["pos"].(map[string]any)
		if !ok {
			t.Fatalf("%s: adjust.pos hilang (adjust: %v)", endpoint, got)
		}
		if pos["x"] != float64(0) || pos["y"] != -0.3 || pos["z"] != float64(0) {
			t.Errorf("%s: adjust.pos = %v, ingin {0,-0.3,0}", endpoint, pos)
		}
		if got["rot"] != nil || got["scale"] != nil {
			t.Errorf("%s: adjust.rot/scale = %v/%v, ingin dua-duanya null",
				endpoint, got["rot"], got["scale"])
		}
	}
	if !seen {
		t.Fatalf("%s: item slot Hair tidak ada", endpoint)
	}
}
