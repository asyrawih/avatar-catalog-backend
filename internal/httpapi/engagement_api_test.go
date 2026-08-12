package httpapi_test

import (
	"net/http"
	"testing"
)

// asPemain menandai request sebagai milik satu pemain. UnverifiedActorAuth
// membaca X-User-Id, jadi ini cukup untuk menghadirkan aktor di test.
func asPemain(userID string) map[string]string {
	return map[string]string{"X-User-Id": userID}
}

func TestLikeOutfitMenaikkanHitungan(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits/" + seedOutfit + "/likes",
		headers: asPemain(seedUser),
	})

	requireStatus(t, resp, body, http.StatusOK)
	if body["changed"] != true || body["liked"] != true {
		t.Errorf("changed=%v liked=%v, ingin true dan true", body["changed"], body["liked"])
	}
	if body["likeCount"] != float64(1) {
		t.Errorf("likeCount = %v, ingin 1", body["likeCount"])
	}
}

// Tombol suka yang ditekan dua kali tetap 200 dengan hitungan yang sama —
// bukan 409 — supaya klien tidak perlu menebak keadaan sebelumnya.
func TestLikeOutfitBerulangTidakMenggandakan(t *testing.T) {
	srv := newTestServer(t)
	req := request{
		method:  http.MethodPost,
		path:    "/v1/outfits/" + seedOutfit + "/likes",
		headers: asPemain(seedUser),
	}

	if resp, body := do(t, srv, req); resp.StatusCode != http.StatusOK {
		t.Fatalf("like pertama status = %d (body: %v)", resp.StatusCode, body)
	}

	resp, body := do(t, srv, req)
	requireStatus(t, resp, body, http.StatusOK)
	if body["changed"] != false {
		t.Errorf("changed = %v pada like berulang, ingin false", body["changed"])
	}
	if body["likeCount"] != float64(1) {
		t.Errorf("likeCount = %v, ingin tetap 1", body["likeCount"])
	}
}

func TestUnlikeOutfitMenurunkanHitungan(t *testing.T) {
	srv := newTestServer(t)

	if resp, body := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits/" + seedOutfit + "/likes",
		headers: asPemain(seedUser),
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("like status = %d (body: %v)", resp.StatusCode, body)
	}

	resp, body := do(t, srv, request{
		method:  http.MethodDelete,
		path:    "/v1/outfits/" + seedOutfit + "/likes",
		headers: asPemain(seedUser),
	})

	requireStatus(t, resp, body, http.StatusOK)
	if body["liked"] != false || body["likeCount"] != float64(0) {
		t.Errorf("liked=%v likeCount=%v, ingin false dan 0", body["liked"], body["likeCount"])
	}
}

func TestLikeTanpaIdentitasDitolak(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits/" + seedOutfit + "/likes",
	})

	requireStatus(t, resp, body, http.StatusUnauthorized)
	requireErrorCode(t, body, "actor_required")
}

func TestLikeOutfitTidakAdaMelaporkan404(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits/otf_tidak_ada/likes",
		headers: asPemain(seedUser),
	})

	requireStatus(t, resp, body, http.StatusNotFound)
	requireErrorCode(t, body, "outfit_not_found")
}

func TestRecordViewMenaikkanHitungan(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits/" + seedOutfit + "/views",
		headers: asPemain(seedUser),
	})

	requireStatus(t, resp, body, http.StatusOK)
	if body["viewCount"] != float64(1) {
		t.Errorf("viewCount = %v, ingin 1", body["viewCount"])
	}
}

// View boleh dicatat tanpa login: masih berguna untuk popularitas, walau tidak
// jadi sinyal per pemain.
func TestRecordViewAnonimDiterima(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{
		method: http.MethodPost,
		path:   "/v1/outfits/" + seedOutfit + "/views",
	})

	requireStatus(t, resp, body, http.StatusOK)
	if body["viewCount"] != float64(1) {
		t.Errorf("viewCount = %v, ingin 1", body["viewCount"])
	}
}

// GET tetap murni baca: membuka detail outfit tidak boleh diam-diam menaikkan
// viewCount, kalau tidak endpoint itu tidak aman di-cache maupun diulang.
func TestGetOutfitTidakMenaikkanViewCount(t *testing.T) {
	srv := newTestServer(t)

	for i := 0; i < 3; i++ {
		if resp, body := do(t, srv, request{
			method: http.MethodGet,
			path:   "/v1/outfits/" + seedOutfit,
		}); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET detail status = %d (body: %v)", resp.StatusCode, body)
		}
	}

	resp, body := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits/" + seedOutfit})
	requireStatus(t, resp, body, http.StatusOK)
	if body["viewCount"] != float64(0) {
		t.Errorf("viewCount = %v setelah tiga kali GET, ingin 0", body["viewCount"])
	}
}

func TestDaftarOutfitMembawaHitunganPopularitas(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?userId=" + seedUser})
	requireStatus(t, resp, body, http.StatusOK)

	data, _ := body["data"].([]any)
	if len(data) == 0 {
		t.Fatal("daftar kosong")
	}
	first, _ := data[0].(map[string]any)
	for _, field := range []string{"likeCount", "viewCount"} {
		if _, ok := first[field]; !ok {
			t.Errorf("ringkasan outfit tidak punya field %q", field)
		}
	}
}

// likedByMe hanya muncul untuk pemanggil yang dikenali. Permintaan anonim tidak
// membawanya sama sekali, bukan membawanya bernilai false — "tidak tahu" beda
// dari "tidak suka".
func TestLikedByMeHanyaUntukPemanggilDikenali(t *testing.T) {
	srv := newTestServer(t)

	if resp, body := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits/" + seedOutfit + "/likes",
		headers: asPemain(seedUser),
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("like status = %d (body: %v)", resp.StatusCode, body)
	}

	resp, body := do(t, srv, request{
		method:  http.MethodGet,
		path:    "/v1/outfits?outfitId=" + seedOutfit,
		headers: asPemain(seedUser),
	})
	requireStatus(t, resp, body, http.StatusOK)
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %v, ingin satu outfit", body["data"])
	}
	if first, _ := data[0].(map[string]any); first["likedByMe"] != true {
		t.Errorf("likedByMe = %v, ingin true", first["likedByMe"])
	}

	resp, body = do(t, srv, request{
		method: http.MethodGet,
		path:   "/v1/outfits?outfitId=" + seedOutfit,
	})
	requireStatus(t, resp, body, http.StatusOK)
	data, _ = body["data"].([]any)
	first, _ := data[0].(map[string]any)
	if _, ada := first["likedByMe"]; ada {
		t.Errorf("likedByMe ikut terkirim untuk pemanggil anonim: %v", first["likedByMe"])
	}
}

func TestDaftarSortMostLiked(t *testing.T) {
	srv := newTestServer(t)

	// seedOutfit2 disukai, seedOutfit tidak — jadi urutannya bukan kebetulan
	// sama dengan urutan terbaru.
	if resp, body := do(t, srv, request{
		method:  http.MethodPost,
		path:    "/v1/outfits/" + seedOutfit2 + "/likes",
		headers: asPemain(seedUser),
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("like status = %d (body: %v)", resp.StatusCode, body)
	}

	resp, body := do(t, srv, request{
		method: http.MethodGet,
		path:   "/v1/outfits?userId=" + seedUser + "&sort=mostLiked",
	})
	requireStatus(t, resp, body, http.StatusOK)

	data, _ := body["data"].([]any)
	if len(data) == 0 {
		t.Fatal("daftar kosong")
	}
	first, _ := data[0].(map[string]any)
	if first["outfitId"] != seedOutfit2 {
		t.Errorf("puncak = %v, ingin %s", first["outfitId"], seedOutfit2)
	}
	if first["likeCount"] != float64(1) {
		t.Errorf("likeCount puncak = %v, ingin 1", first["likeCount"])
	}
}

func TestDaftarSortTidakDikenalDitolak(t *testing.T) {
	srv := newTestServer(t)

	resp, body := do(t, srv, request{method: http.MethodGet, path: "/v1/outfits?sort=terpopuler"})

	requireStatus(t, resp, body, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_sort")
}
