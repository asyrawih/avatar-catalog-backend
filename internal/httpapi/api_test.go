package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/httpapi"
	"github.com/hanan/avatar-catalog-backend/internal/idempotency"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

const (
	assetHair   = float64(78872304386489)
	assetJacket = float64(14433369343)
	seedOutfit  = "otf_9f2a41"
	seedOutfit2 = "otf_3c88de"
	seedUser    = "627278822"

	// devRig sudah terdaftar di data contoh; newRig belum pernah dilihat backend.
	devRig = "88484288792766"
	newRig = "77771111222233"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	outfits := store.NewMemoryOutfits()
	templates := store.NewMemoryTemplates()
	transactions := store.NewMemoryTransactions()
	store.SeedData(templates, outfits)

	handler := httpapi.NewRouter(httpapi.Deps{
		Outfits:      service.NewOutfits(outfits, templates),
		Transactions: service.NewTransactions(transactions),
		Templates:    service.NewTemplates(templates),
		Idempotency:  idempotency.NewMemoryStore(time.Hour),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

type request struct {
	method  string
	path    string
	body    any
	headers map[string]string
}

// do menjalankan satu request dan mengembalikan respons beserta body JSON-nya.
func do(t *testing.T, srv *httptest.Server, req request) (*http.Response, map[string]any) {
	t.Helper()

	var reader io.Reader
	if req.body != nil {
		raw, err := json.Marshal(req.body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	httpReq, err := http.NewRequest(req.method, srv.URL+req.path, reader)
	if err != nil {
		t.Fatalf("bikin request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range req.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := srv.Client().Do(httpReq)
	if err != nil {
		t.Fatalf("%s %s: %v", req.method, req.path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("baca body: %v", err)
	}

	var payload map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("body bukan objek JSON (%s): %s", err, raw)
		}
	}
	return resp, payload
}

func requireStatus(t *testing.T, resp *http.Response, body map[string]any, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, ingin %d (body: %v)", resp.StatusCode, want, body)
	}
}

// requireErrorCode menegaskan amplop error memakai kode tertentu.
func requireErrorCode(t *testing.T, body map[string]any, want string) {
	t.Helper()

	envelope, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("body bukan amplop error: %v", body)
	}
	if envelope["code"] != want {
		t.Errorf("error.code = %v, ingin %q", envelope["code"], want)
	}
	if envelope["message"] == "" || envelope["message"] == nil {
		t.Error("error.message kosong")
	}
}

func createOutfitBody() map[string]any {
	return map[string]any{
		"userId":     627278822,
		"templateId": devRig,
		"name":       "Y2K Streetwear",
		"isPublic":   false,
		"customTags": []string{"category:y2k", "gender:male"},
		"items": []map[string]any{
			{"assetId": assetHair, "slot": "Hair"},
			{"assetId": assetJacket, "slot": "Jacket"},
		},
	}
}

// TestCreateOutfitMenerimaBentukHasilGet menjaga round-trip GET lalu POST tetap
// jalan: klien yang mengirim balik item hasil GET apa adanya — lengkap dengan
// name, assetType, dan price — tidak boleh ditolak sebagai JSON tidak dikenal.
func TestCreateOutfitMenerimaBentukHasilGet(t *testing.T) {
	srv := newTestServer(t)

	resp, detail := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + seedOutfit})
	requireStatus(t, resp, detail, http.StatusOK)

	items, _ := detail["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("items = %v, ingin outfit contoh punya isi", detail["items"])
	}

	body := createOutfitBody()
	body["items"] = items

	resp, created := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits",
		body:    body,
		headers: map[string]string{"Idempotency-Key": "24127f4e-d2fe-498a-a935-b1fcbdc371ca"},
	})

	requireStatus(t, resp, created, http.StatusCreated)
	if created["outfitId"] == nil {
		t.Errorf("body = %v, ingin outfitId", created)
	}
}

// TestCreateOutfitMenyimpanDetailItem memastikan name, assetType, dan price yang
// dikirim klien tersimpan di outfit dan terbaca kembali lewat GET.
func TestCreateOutfitMenyimpanDetailItem(t *testing.T) {
	srv := newTestServer(t)

	const newAsset = float64(120044550099)
	body := createOutfitBody()
	body["items"] = []map[string]any{
		{"assetId": newAsset, "slot": "Hat", "name": "Chrome Visor",
			"assetType": "Accessory", "price": 149},
	}

	resp, created := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits",
		body:    body,
		headers: map[string]string{"Idempotency-Key": "a4e1f0c9-1111-2222-3333-444455556666"},
	})
	requireStatus(t, resp, created, http.StatusCreated)

	resp, detail := do(t, srv, request{
		method: http.MethodGet,
		path:   "/v1/outfits/" + created["outfitId"].(string),
	})
	requireStatus(t, resp, detail, http.StatusOK)

	items, _ := detail["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v, ingin satu item", detail["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["name"] != "Chrome Visor" || item["assetType"] != "Accessory" || item["price"] != float64(149) {
		t.Errorf("item = %v, ingin name Chrome Visor, assetType Accessory, price 149", item)
	}
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{method: http.MethodGet, path: "/healthz"})

	requireStatus(t, resp, body, http.StatusOK)
	if body["status"] != "ok" {
		t.Errorf("status = %v, ingin ok", body["status"])
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Error("header X-Request-ID tidak diisi")
	}
}

func TestListOutfits(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?userId=" + seedUser})

	requireStatus(t, resp, body, http.StatusOK)
	data, ok := body["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("data = %v, ingin 2 outfit contoh", body["data"])
	}
	if body["hasMore"] != false || body["nextCursor"] != nil {
		t.Errorf("hasMore=%v nextCursor=%v, ingin false dan null", body["hasMore"], body["nextCursor"])
	}

	first, _ := data[0].(map[string]any)
	for _, field := range []string{"outfitId", "referenceId", "name", "templateId", "isPublic", "itemCount", "items", "updatedAt"} {
		if _, ok := first[field]; !ok {
			t.Errorf("ringkasan outfit tidak punya field %q", field)
		}
	}
}

// Daftar membawa item lengkap supaya klien tidak perlu menyusul GET detail per
// outfit hanya untuk tahu isinya.
func TestListOutfitsMembawaDetailItem(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?userId=" + seedUser})
	requireStatus(t, resp, body, http.StatusOK)

	data, _ := body["data"].([]any)
	var seeded map[string]any
	for _, raw := range data {
		if row, _ := raw.(map[string]any); row["outfitId"] == seedOutfit {
			seeded = row
		}
	}
	if seeded == nil {
		t.Fatalf("outfit contoh %s tidak ada di daftar", seedOutfit)
	}

	items, _ := seeded["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("jumlah item = %d, ingin 3 — sama dengan GET detail", len(items))
	}
	if seeded["itemCount"] != float64(len(items)) {
		t.Errorf("itemCount = %v, ingin %d", seeded["itemCount"], len(items))
	}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["assetId"] != assetHair {
			continue
		}
		if item["name"] != "BLOND BARREL TWISTS DREADS" ||
			item["assetType"] != "HairAccessory" || item["price"] != float64(69) {
			t.Errorf("item = %v, ingin detail selengkap GET", item)
		}
	}
}

func TestListOutfitsTanpaUserIDMencakupSemuaPemain(t *testing.T) {
	srv := newTestServer(t)

	// Outfit milik pemain lain supaya daftar gabungan benar-benar gabungan.
	body := createOutfitBody()
	body["userId"] = 999111
	resp, created := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: body})
	requireStatus(t, resp, created, http.StatusCreated)

	resp, all := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits"})
	requireStatus(t, resp, all, http.StatusOK)
	data, _ := all["data"].([]any)
	if len(data) != 3 {
		t.Fatalf("data = %d outfit, ingin 3 (2 contoh + 1 pemain lain)", len(data))
	}

	// Dengan userId, daftar kembali menyempit ke pemain itu saja.
	resp, mine := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?userId=" + seedUser})
	requireStatus(t, resp, mine, http.StatusOK)
	mineData, _ := mine["data"].([]any)
	if len(mineData) != 2 {
		t.Errorf("data = %d outfit, ingin 2 milik pemain contoh", len(mineData))
	}
}

func TestListOutfitsFilterIsPublic(t *testing.T) {
	srv := newTestServer(t)

	resp, publik := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?isPublic=true"})
	requireStatus(t, resp, publik, http.StatusOK)
	data, _ := publik["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %d outfit, ingin 1 yang publik", len(data))
	}
	row, _ := data[0].(map[string]any)
	if row["isPublic"] != true {
		t.Errorf("outfit = %v, ingin isPublic true", row)
	}

	resp, salah := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?isPublic=entah"})
	requireStatus(t, resp, salah, http.StatusBadRequest)
	requireErrorCode(t, salah, "invalid_query")
}

func TestListOutfitsCariKeyword(t *testing.T) {
	srv := newTestServer(t)

	// Cocok sebagian dan tanpa peduli huruf besar-kecil.
	resp, body := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?q=streetwear"})
	requireStatus(t, resp, body, http.StatusOK)
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %d outfit, ingin 1 yang namanya memuat streetwear", len(data))
	}
	row, _ := data[0].(map[string]any)
	if row["name"] != "Y2K Streetwear" {
		t.Errorf("name = %v, ingin Y2K Streetwear", row["name"])
	}

	// Wildcard diperlakukan sebagai teks biasa, bukan pola.
	resp, wildcard := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?q=%25"})
	requireStatus(t, resp, wildcard, http.StatusOK)
	if data, _ := wildcard["data"].([]any); len(data) != 0 {
		t.Errorf("data = %d outfit, ingin 0 — %% tidak boleh jadi wildcard", len(data))
	}

	// Keyword bisa dipadukan dengan filter lain.
	resp, sempit := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?q=pop&isPublic=false"})
	requireStatus(t, resp, sempit, http.StatusOK)
	if data, _ := sempit["data"].([]any); len(data) != 1 {
		t.Errorf("data = %d outfit, ingin 1 hasil q=pop yang privat", len(data))
	}

	resp, kepanjangan := do(t, srv, request{
		method: http.MethodGet,
		path:   "/v1/outfits?q=" + strings.Repeat("a", service.MaxKeywordLen+1),
	})
	requireStatus(t, resp, kepanjangan, http.StatusBadRequest)
	requireErrorCode(t, kepanjangan, "invalid_keyword")
}

func TestListOutfitsFilterOutfitID(t *testing.T) {
	srv := newTestServer(t)

	resp, satu := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?outfitId=" + seedOutfit})
	requireStatus(t, resp, satu, http.StatusOK)
	data, _ := satu["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %d outfit, ingin 1", len(data))
	}
	if row, _ := data[0].(map[string]any); row["outfitId"] != seedOutfit {
		t.Errorf("outfitId = %v, ingin %s", row, seedOutfit)
	}

	// Beberapa id sekaligus, dipisah koma maupun diulang.
	for _, path := range []string{
		"/v1/outfits?outfitId=" + seedOutfit + "," + seedOutfit2,
		"/v1/outfits?outfitId=" + seedOutfit + "&outfitId=" + seedOutfit2,
	} {
		resp, banyak := do(t, srv, request{method: http.MethodGet, path: path})
		requireStatus(t, resp, banyak, http.StatusOK)
		if data, _ := banyak["data"].([]any); len(data) != 2 {
			t.Errorf("%s: data = %d outfit, ingin 2", path, len(data))
		}
	}

	// Id yang tidak ada menghasilkan daftar kosong, bukan galat.
	resp, kosong := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?outfitId=otf_tidakada"})
	requireStatus(t, resp, kosong, http.StatusOK)
	if data, _ := kosong["data"].([]any); len(data) != 0 {
		t.Errorf("data = %d outfit, ingin 0", len(data))
	}
}

func TestGetOutfitMembawaDetailItem(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + seedOutfit})

	requireStatus(t, resp, body, http.StatusOK)

	items, _ := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("jumlah item = %d, ingin 3", len(items))
	}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["assetId"] != assetHair {
			continue
		}
		if item["name"] != "BLOND BARREL TWISTS DREADS" ||
			item["assetType"] != "HairAccessory" || item["price"] != float64(69) {
			t.Errorf("item = %v, ingin detail lengkap", item)
		}
	}
}

func TestOutfitCRUDLewatHTTP(t *testing.T) {
	srv := newTestServer(t)

	resp, created := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits",
		body:    createOutfitBody(),
		headers: map[string]string{"Idempotency-Key": "buat-outfit-1"},
	})
	requireStatus(t, resp, created, http.StatusCreated)

	outfitID, _ := created["outfitId"].(string)
	if outfitID == "" || created["referenceId"] == "" {
		t.Fatalf("respons create tidak lengkap: %v", created)
	}
	if created["recoItemId"] != nil {
		t.Errorf("recoItemId = %v, ingin null sebelum RegisterItemAsync", created["recoItemId"])
	}
	if got := resp.Header.Get("Location"); got != "/v1/outfits/"+outfitID {
		t.Errorf("Location = %q, ingin /v1/outfits/%s", got, outfitID)
	}

	// PATCH menyimpan recoItemId hasil RegisterItemAsync.
	resp, patched := do(t, srv, request{
		method: http.MethodPatch,
		path:   "/v1/outfits/" + outfitID,
		body:   map[string]any{"isPublic": true, "recoItemId": "reco_7b31c9"},
	})
	requireStatus(t, resp, patched, http.StatusOK)
	if patched["recoItemId"] != "reco_7b31c9" || patched["isPublic"] != true {
		t.Errorf("hasil patch = %v", patched)
	}

	// PUT mengganti seluruh isi item.
	resp, replaced := do(t, srv, request{
		method: http.MethodPut,
		path:   "/v1/outfits/" + outfitID + "/items",
		body: map[string]any{"items": []map[string]any{
			{"assetId": assetHair, "slot": "Hair"},
		}},
	})
	requireStatus(t, resp, replaced, http.StatusOK)
	if replaced["itemCount"] != float64(1) || replaced["replaced"] != true {
		t.Errorf("hasil replace = %v", replaced)
	}

	// DELETE hanya mengisi deletedAt dan mengingatkan RemoveItemAsync.
	resp, deleted := do(t, srv, request{method: http.MethodDelete, path: "/v1/outfits/" + outfitID})
	requireStatus(t, resp, deleted, http.StatusOK)
	if deleted["deletedAt"] == nil || deleted["reminder"] == nil {
		t.Errorf("hasil delete = %v", deleted)
	}

	resp, gone := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + outfitID})
	requireStatus(t, resp, gone, http.StatusGone)
	requireErrorCode(t, gone, "outfit_deleted")
}

func TestCreateOutfitMengulangIdempotencyKey(t *testing.T) {
	srv := newTestServer(t)
	headers := map[string]string{"Idempotency-Key": "kunci-sama"}

	resp, first := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: createOutfitBody(), headers: headers})
	requireStatus(t, resp, first, http.StatusCreated)

	resp, second := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: createOutfitBody(), headers: headers})

	requireStatus(t, resp, second, http.StatusOK)
	if second["idempotentReplay"] != true {
		t.Error("idempotentReplay tidak ditandai pada pengulangan")
	}
	if second["outfitId"] != first["outfitId"] || second["referenceId"] != first["referenceId"] {
		t.Errorf("pengulangan membuat outfit baru: %v vs %v", second, first)
	}
}

func TestCreateOutfitTagBerkoma(t *testing.T) {
	srv := newTestServer(t)
	body := createOutfitBody()
	body["customTags"] = []string{"category:y2k,street"}

	resp, payload := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: body})

	requireStatus(t, resp, payload, http.StatusUnprocessableEntity)
	requireErrorCode(t, payload, "invalid_custom_tag")

	envelope, _ := payload["error"].(map[string]any)
	details, _ := envelope["details"].([]any)
	if len(details) != 1 {
		t.Fatalf("details = %v, ingin satu entri", envelope["details"])
	}
	detail, _ := details[0].(map[string]any)
	if detail["field"] != "customTags[0]" {
		t.Errorf("field = %v, ingin customTags[0]", detail["field"])
	}
}

func TestCreateOutfitMendaftarkanRigBaru(t *testing.T) {
	srv := newTestServer(t)

	// Rig baru saja di-upload ke Roblox, backend belum pernah melihatnya.
	resp, payload := do(t, srv, request{method: http.MethodGet, path: "/v1/templates/" + newRig})
	requireStatus(t, resp, payload, http.StatusNotFound)

	body := createOutfitBody()
	body["templateId"] = newRig
	resp, created := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: body})
	requireStatus(t, resp, created, http.StatusCreated)

	resp, tpl := do(t, srv, request{method: http.MethodGet, path: "/v1/templates/" + newRig})
	requireStatus(t, resp, tpl, http.StatusOK)
	if tpl["sourceAssetId"] != float64(77771111222233) {
		t.Errorf("sourceAssetId = %v, ingin 77771111222233", tpl["sourceAssetId"])
	}
	if tpl["gender"] != "?" || tpl["name"] != "" {
		t.Errorf("rig otomatis = %v, ingin nama kosong dan gender '?'", tpl)
	}
}

func TestCreateOutfitMenerimaTemplateIDBerupaAngka(t *testing.T) {
	srv := newTestServer(t)

	// Klien Luau yang menyimpan asset id sebagai number akan mengirim angka
	// telanjang lewat JSONEncode.
	body := createOutfitBody()
	body["templateId"] = float64(88484288792766)

	resp, created := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: body})

	requireStatus(t, resp, created, http.StatusCreated)
	resp, detail := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + created["outfitId"].(string)})
	requireStatus(t, resp, detail, http.StatusOK)
	if detail["templateId"] != devRig {
		t.Errorf("templateId = %v, ingin %q", detail["templateId"], devRig)
	}
}

func TestCreateOutfitMenolakTemplateIDBukanAssetID(t *testing.T) {
	srv := newTestServer(t)
	body := createOutfitBody()
	body["templateId"] = "male_2"

	resp, payload := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: body})

	requireStatus(t, resp, payload, http.StatusUnprocessableEntity)
	requireErrorCode(t, payload, "invalid_template_id")
}

func TestTemplateRegistryLewatHTTP(t *testing.T) {
	srv := newTestServer(t)

	resp, registered := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/templates",
		body:   map[string]any{"templateId": newRig, "name": "Rig Cewek V2", "gender": "F"},
	})
	requireStatus(t, resp, registered, http.StatusCreated)
	if got := resp.Header.Get("Location"); got != "/v1/templates/"+newRig {
		t.Errorf("Location = %q", got)
	}

	// Pendaftaran ulang aman dan tidak menimpa.
	resp, again := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/templates",
		body:   map[string]any{"templateId": newRig, "name": "Nama Lain"},
	})
	requireStatus(t, resp, again, http.StatusOK)
	if again["name"] != "Rig Cewek V2" {
		t.Errorf("name = %v, ingin tetap Rig Cewek V2", again["name"])
	}

	resp, patched := do(t, srv, request{
		method: http.MethodPatch,
		path:   "/v1/templates/" + newRig,
		body:   map[string]any{"gender": "M"},
	})
	requireStatus(t, resp, patched, http.StatusOK)
	if patched["gender"] != "M" || patched["name"] != "Rig Cewek V2" {
		t.Errorf("hasil patch = %v", patched)
	}

	resp, list := do(t, srv, request{method: http.MethodGet, path: "/v1/templates"})
	requireStatus(t, resp, list, http.StatusOK)
	data, _ := list["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("jumlah rig = %d, ingin 2 (dev rig + rig baru)", len(data))
	}
}

func TestTemplateRegistryMenolakIDBukanAssetID(t *testing.T) {
	srv := newTestServer(t)

	resp, payload := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/templates",
		body:   map[string]any{"templateId": "male_2"},
	})

	requireStatus(t, resp, payload, http.StatusUnprocessableEntity)
	requireErrorCode(t, payload, "invalid_template_id")
}

func TestCreateOutfitSlotGandaDiterima(t *testing.T) {
	srv := newTestServer(t)
	body := createOutfitBody()
	body["items"] = []map[string]any{
		{"assetId": assetHair, "slot": "Hair"},
		{"assetId": assetJacket, "slot": "Hair"},
	}

	resp, created := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: body})
	requireStatus(t, resp, created, http.StatusCreated)

	outfitID, _ := created["outfitId"].(string)
	resp, detail := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + outfitID})
	requireStatus(t, resp, detail, http.StatusOK)

	items, _ := detail["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("jumlah item = %d, ingin 2 (dua asset di slot Hair)", len(items))
	}
	for _, raw := range items {
		if item, _ := raw.(map[string]any); item["slot"] != "Hair" {
			t.Errorf("slot = %v, ingin Hair", item["slot"])
		}
	}
}

// POST menerima body avatar dan GET mengembalikannya dalam bentuk yang sama
// persis, jadi hasil GET bisa dikirim balik apa adanya.
func TestCreateOutfitMenyimpanBodyAvatar(t *testing.T) {
	srv := newTestServer(t)
	body := createOutfitBody()
	body["body"] = map[string]any{
		"colors": map[string]any{
			"head": "AE7C64", "torso": "AE7C64",
			"leftArm": "AE7C64", "rightArm": "AE7C64",
			"leftLeg": "AE7C64", "rightLeg": "AE7C64",
		},
		"scales": map[string]any{
			"height": 1.0499999523162842, "width": 1, "head": 1,
			"depth": 1, "bodyType": 1, "proportion": 0,
		},
	}

	resp, created := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: body})
	requireStatus(t, resp, created, http.StatusCreated)

	outfitID, _ := created["outfitId"].(string)
	resp, detail := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + outfitID})
	requireStatus(t, resp, detail, http.StatusOK)

	got, _ := detail["body"].(map[string]any)
	if got == nil {
		t.Fatalf("body = %v, ingin ikut dikembalikan", detail["body"])
	}
	colors, _ := got["colors"].(map[string]any)
	if colors["head"] != "AE7C64" || colors["rightLeg"] != "AE7C64" {
		t.Errorf("body.colors = %v", got["colors"])
	}
	scales, _ := got["scales"].(map[string]any)
	if scales["height"] != 1.0499999523162842 {
		t.Errorf("body.scales.height = %v, ingin tanpa pembulatan", scales["height"])
	}
	if scales["proportion"] != float64(0) || scales["bodyType"] != float64(1) {
		t.Errorf("body.scales = %v", got["scales"])
	}
}

// Outfit tanpa body dijawab `"body": null`, bukan objek berisi nol.
func TestOutfitTanpaBodyDijawabNull(t *testing.T) {
	srv := newTestServer(t)

	resp, detail := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + seedOutfit})
	requireStatus(t, resp, detail, http.StatusOK)

	raw, ok := detail["body"]
	if !ok {
		t.Fatal("field body tidak ada di respons")
	}
	if raw != nil {
		t.Errorf("body = %v, ingin null", raw)
	}
}

func TestCreateOutfitMenolakWarnaBodyNgawur(t *testing.T) {
	srv := newTestServer(t)
	body := createOutfitBody()
	body["body"] = map[string]any{"colors": map[string]any{"head": "merah"}}

	resp, payload := do(t, srv, request{method: http.MethodPost, path: "/v1/outfits", body: body})

	requireStatus(t, resp, payload, http.StatusUnprocessableEntity)
	requireErrorCode(t, payload, "invalid_body_color")
}

func TestPatchOutfitBukanPemilik(t *testing.T) {
	srv := newTestServer(t)

	resp, payload := do(t, srv, request{
		method:  http.MethodPatch,
		path:    "/v1/outfits/" + seedOutfit,
		body:    map[string]any{"isPublic": true},
		headers: map[string]string{"X-User-Id": "111"},
	})

	requireStatus(t, resp, payload, http.StatusForbidden)
	requireErrorCode(t, payload, "not_owner")
}

func TestResolveOutfits(t *testing.T) {
	srv := newTestServer(t)

	resp, payload := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits/resolve",
		body: map[string]any{"referenceIds": []string{
			"550e8400-e29b-41d4-a716-446655440000",
			"00000000-0000-0000-0000-000000000000",
		}},
	})

	requireStatus(t, resp, payload, http.StatusOK)
	data, _ := payload["data"].([]any)
	notFound, _ := payload["notFound"].([]any)
	if len(data) != 1 {
		t.Errorf("data = %v, ingin satu outfit", payload["data"])
	}
	if len(notFound) != 1 || notFound[0] != "00000000-0000-0000-0000-000000000000" {
		t.Errorf("notFound = %v", payload["notFound"])
	}
}

func TestTransactionMembutuhkanIdempotencyKey(t *testing.T) {
	srv := newTestServer(t)

	resp, payload := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/transactions",
		body: map[string]any{
			"userId": 627278822,
			"status": "success",
			"items":  []map[string]any{{"assetId": assetHair, "price": 69, "result": "success"}},
		},
	})

	requireStatus(t, resp, payload, http.StatusBadRequest)
	requireErrorCode(t, payload, "missing_idempotency_key")
}

func TestTransactionCreateLaluReplayLaluList(t *testing.T) {
	srv := newTestServer(t)
	body := map[string]any{
		"userId":     627278822,
		"universeId": 8739264611,
		"placeId":    124012682058672,
		"jobId":      "b1f0e2c4-7a3d-4c11-9f88-2ab5c9e01d77",
		"status":     "success",
		"occurredAt": "2026-08-04T11:22:03Z",
		"items": []map[string]any{
			{"assetId": assetHair, "price": 69, "result": "success"},
			{"assetId": assetJacket, "price": 79, "result": "aborted"},
		},
	}
	headers := map[string]string{"Idempotency-Key": "tx-key-1"}

	resp, created := do(t, srv, request{method: http.MethodPost, path: "/v1/transactions", body: body, headers: headers})
	requireStatus(t, resp, created, http.StatusCreated)
	if created["robuxTotal"] != float64(69) {
		t.Errorf("robuxTotal = %v, ingin 69 (item aborted tidak dihitung)", created["robuxTotal"])
	}

	resp, replay := do(t, srv, request{method: http.MethodPost, path: "/v1/transactions", body: body, headers: headers})
	requireStatus(t, resp, replay, http.StatusOK)
	if replay["idempotentReplay"] != true || replay["txId"] != created["txId"] {
		t.Errorf("replay = %v, ingin txId sama dengan %v", replay, created["txId"])
	}

	resp, list := do(t, srv, request{method: http.MethodGet, path: "/v1/transactions?userId=" + seedUser})
	requireStatus(t, resp, list, http.StatusOK)
	data, _ := list["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %v, ingin satu transaksi", list["data"])
	}
	row, _ := data[0].(map[string]any)
	if row["itemCount"] != float64(2) || row["robuxTotal"] != float64(69) {
		t.Errorf("ringkasan transaksi = %v", row)
	}
}

func TestBodyJSONTidakValid(t *testing.T) {
	srv := newTestServer(t)

	resp, payload := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits",
		body:   map[string]any{"tidakDikenal": 1},
	})

	requireStatus(t, resp, payload, http.StatusBadRequest)
	requireErrorCode(t, payload, "invalid_json")
}

func TestRuteTidakDikenal(t *testing.T) {
	srv := newTestServer(t)

	resp, payload := do(t, srv, request{method: http.MethodGet, path: "/v1/entah"})

	requireStatus(t, resp, payload, http.StatusNotFound)
	requireErrorCode(t, payload, "route_not_found")
}
