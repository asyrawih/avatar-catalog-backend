package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/service"
)

// batchOutfitBody menyusun satu elemen batch dengan nama yang berbeda-beda,
// supaya hasilnya bisa dicocokkan kembali ke posisinya.
func batchOutfitBody(name string) map[string]any {
	body := createOutfitBody()
	body["name"] = name
	return body
}

// results membaca array results dari balasan batch.
func batchResults(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()

	raw, ok := body["results"].([]any)
	if !ok {
		t.Fatalf("results bukan array: %v", body)
	}
	out := make([]map[string]any, 0, len(raw))
	for i, entry := range raw {
		row, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("results[%d] bukan objek: %v", i, entry)
		}
		out = append(out, row)
	}
	return out
}

// TestBatchCreateMenyimpanSemuanya adalah alasan endpoint ini ada: satu
// permintaan, banyak outfit, dan tiap outfit benar-benar tersimpan lengkap
// dengan itemnya.
func TestBatchCreateMenyimpanSemuanya(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits:batch",
		body: map[string]any{"outfits": []map[string]any{
			batchOutfitBody("Batch Satu"),
			batchOutfitBody("Batch Dua"),
			batchOutfitBody("Batch Tiga"),
		}},
	})
	requireStatus(t, resp, body, http.StatusCreated)

	if body["created"] != float64(3) || body["failed"] != float64(0) {
		t.Fatalf("created/failed = %v/%v, ingin 3/0", body["created"], body["failed"])
	}

	rows := batchResults(t, body)
	if len(rows) != 3 {
		t.Fatalf("results = %d entri, ingin 3", len(rows))
	}

	seen := map[string]bool{}
	for i, row := range rows {
		if row["index"] != float64(i) {
			t.Errorf("results[%d].index = %v, ingin %d", i, row["index"], i)
		}
		outfitID, _ := row["outfitId"].(string)
		if outfitID == "" {
			t.Fatalf("results[%d] tanpa outfitId: %v", i, row)
		}
		if ref, _ := row["referenceId"].(string); ref == "" {
			t.Errorf("results[%d] tanpa referenceId: %v", i, row)
		}
		if seen[outfitID] {
			t.Errorf("outfitId %s terpakai dua kali", outfitID)
		}
		seen[outfitID] = true

		// Tersimpan sungguhan, bukan cuma dilaporkan tersimpan.
		resp, detail := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + outfitID})
		requireStatus(t, resp, detail, http.StatusOK)
		if items, _ := detail["items"].([]any); len(items) != 2 {
			t.Errorf("outfit %s punya %d item, ingin 2", outfitID, len(items))
		}
	}
}

// TestBatchCreateSebagianGagal menjaga janji utama endpoint ini: satu outfit
// cacat tidak menahan yang lain, dan yang gagal ditunjuk lewat index-nya.
func TestBatchCreateSebagianGagal(t *testing.T) {
	srv := newTestServer(t)

	rusak := batchOutfitBody("") // name kosong → ditolak
	rusak["userId"] = 627278822  // sisanya sah, supaya yang gagal hanya name

	resp, body := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits:batch",
		body: map[string]any{"outfits": []map[string]any{
			batchOutfitBody("Sah Pertama"),
			rusak,
			batchOutfitBody("Sah Kedua"),
		}},
	})
	requireStatus(t, resp, body, http.StatusOK)

	if body["created"] != float64(2) || body["failed"] != float64(1) {
		t.Fatalf("created/failed = %v/%v, ingin 2/1", body["created"], body["failed"])
	}

	rows := batchResults(t, body)
	gagal, ok := rows[1]["error"].(map[string]any)
	if !ok {
		t.Fatalf("results[1] tidak membawa error: %v", rows[1])
	}
	if gagal["code"] != "missing_name" {
		t.Errorf("results[1].error.code = %v, ingin missing_name", gagal["code"])
	}
	if rows[1]["outfitId"] != nil {
		t.Errorf("results[1] tidak boleh punya outfitId: %v", rows[1])
	}
	if rows[0]["outfitId"] == nil || rows[2]["outfitId"] == nil {
		t.Errorf("outfit yang sah harus tetap tersimpan: %v", rows)
	}

	// Yang lolos benar-benar ada, jadi kegagalan tetangganya tidak ikut
	// membatalkan penulisan.
	outfitID := rows[0]["outfitId"].(string)
	resp, detail := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + outfitID})
	requireStatus(t, resp, detail, http.StatusOK)
}

// TestBatchCreateSemuaGagal memastikan batch yang tidak menyisakan satu pun
// baris tersimpan menjawab 422 — bukan 200 yang terbaca seperti berhasil.
func TestBatchCreateSemuaGagal(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits:batch",
		body: map[string]any{"outfits": []map[string]any{
			batchOutfitBody(""),
			batchOutfitBody(""),
		}},
	})
	requireStatus(t, resp, body, http.StatusUnprocessableEntity)

	if body["created"] != float64(0) || body["failed"] != float64(2) {
		t.Fatalf("created/failed = %v/%v, ingin 0/2", body["created"], body["failed"])
	}
	if len(batchResults(t, body)) != 2 {
		t.Errorf("results harus tetap menjelaskan tiap kegagalan: %v", body)
	}
}

// TestBatchCreateBatasJumlah menjaga batas atas tetap ditegakkan, dan batch
// kosong ditolak alih-alih dianggap berhasil tanpa menulis apa pun.
func TestBatchCreateBatasJumlah(t *testing.T) {
	srv := newTestServer(t)

	terlalu := make([]map[string]any, 0, service.MaxBatchOutfits+1)
	for i := range service.MaxBatchOutfits + 1 {
		terlalu = append(terlalu, batchOutfitBody(fmt.Sprintf("Outfit %d", i)))
	}

	resp, body := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits:batch",
		body:   map[string]any{"outfits": terlalu},
	})
	requireStatus(t, resp, body, http.StatusRequestEntityTooLarge)
	requireErrorCode(t, body, "too_many_outfits")

	resp, kosong := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits:batch",
		body:   map[string]any{"outfits": []map[string]any{}},
	})
	requireStatus(t, resp, kosong, http.StatusUnprocessableEntity)
	requireErrorCode(t, kosong, "empty_batch")
}

// TestBatchCreateIdempoten menjaga retry importer tidak menggandakan seluruh
// batch — jaringan yang putus setelah server menulis adalah kejadian biasa
// pada impor panjang.
func TestBatchCreateIdempoten(t *testing.T) {
	srv := newTestServer(t)

	headers := map[string]string{"Idempotency-Key": "impor-batch-1"}
	payload := map[string]any{"outfits": []map[string]any{
		batchOutfitBody("Sekali Saja"),
		batchOutfitBody("Sekali Saja Juga"),
	}}

	resp, pertama := do(t, srv, request{
		method: http.MethodPost, path: "/v1/outfits:batch", body: payload, headers: headers,
	})
	requireStatus(t, resp, pertama, http.StatusCreated)

	// Pengulangan dijawab 200 + idempotentReplay, sama seperti POST /v1/outfits:
	// tidak ada baris baru yang dibuat, jadi 201 akan berbohong.
	resp, kedua := do(t, srv, request{
		method: http.MethodPost, path: "/v1/outfits:batch", body: payload, headers: headers,
	})
	requireStatus(t, resp, kedua, http.StatusOK)
	if kedua["idempotentReplay"] != true {
		t.Errorf("balasan kedua tidak ditandai idempotentReplay: %v", kedua)
	}

	first := batchResults(t, pertama)
	second := batchResults(t, kedua)
	for i := range first {
		if first[i]["outfitId"] != second[i]["outfitId"] {
			t.Fatalf("retry membuat outfit baru di index %d: %v vs %v",
				i, first[i]["outfitId"], second[i]["outfitId"])
		}
	}

	// Dan totalnya memang tidak bertambah dua kali.
	resp, list := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?userId=" + seedUser + "&limit=100"})
	requireStatus(t, resp, list, http.StatusOK)

	data, _ := list["data"].([]any)
	count := 0
	for _, entry := range data {
		row, _ := entry.(map[string]any)
		if name, _ := row["name"].(string); name == "Sekali Saja" || name == "Sekali Saja Juga" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("outfit hasil batch = %d, ingin 2 (retry tidak boleh menggandakan)", count)
	}
}
