package service_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/service"
)

// aktor pembanding untuk test like/view.
var (
	pemain  = service.Actor{UserID: seededUser, Present: true}
	pemain2 = service.Actor{UserID: seededUser + 1, Present: true}
	anonim  = service.Actor{}
)

func TestLikeMenaikkanHitunganDanIdempoten(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	first, err := svc.Like(ctx, pemain, seededOutfit)
	if err != nil {
		t.Fatalf("Like() error = %v", err)
	}
	if !first.Changed || first.LikeCount != 1 || !first.Liked {
		t.Fatalf("like pertama = %+v, ingin changed=true liked=true likeCount=1", first)
	}

	// Tombol suka yang ditekan dua kali harus berakhir pada keadaan yang sama,
	// bukan pada hitungan dua.
	second, err := svc.Like(ctx, pemain, seededOutfit)
	if err != nil {
		t.Fatalf("Like() kedua error = %v", err)
	}
	if second.Changed {
		t.Error("changed = true pada like berulang, ingin false")
	}
	if second.LikeCount != 1 {
		t.Errorf("likeCount = %d setelah like berulang, ingin 1", second.LikeCount)
	}
}

func TestLikeDariPemainBerbedaDihitungSendiri(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	if _, err := svc.Like(ctx, pemain, seededOutfit); err != nil {
		t.Fatalf("Like() error = %v", err)
	}
	second, err := svc.Like(ctx, pemain2, seededOutfit)
	if err != nil {
		t.Fatalf("Like() pemain kedua error = %v", err)
	}
	if second.LikeCount != 2 {
		t.Errorf("likeCount = %d, ingin 2", second.LikeCount)
	}
}

func TestUnlikeMenurunkanHitunganDanIdempoten(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	if _, err := svc.Like(ctx, pemain, seededOutfit); err != nil {
		t.Fatalf("Like() error = %v", err)
	}

	removed, err := svc.Unlike(ctx, pemain, seededOutfit)
	if err != nil {
		t.Fatalf("Unlike() error = %v", err)
	}
	if !removed.Changed || removed.LikeCount != 0 || removed.Liked {
		t.Fatalf("unlike = %+v, ingin changed=true liked=false likeCount=0", removed)
	}

	again, err := svc.Unlike(ctx, pemain, seededOutfit)
	if err != nil {
		t.Fatalf("Unlike() kedua error = %v", err)
	}
	if again.Changed || again.LikeCount != 0 {
		t.Errorf("unlike berulang = %+v, ingin changed=false likeCount=0", again)
	}
}

func TestLikeTanpaAktorDitolak(t *testing.T) {
	svc := newOutfitService(t)

	_, err := svc.Like(context.Background(), anonim, seededOutfit)
	requireAPIError(t, err, http.StatusUnauthorized, "actor_required")
}

func TestLikeOutfitTidakAdaMelaporkan404(t *testing.T) {
	svc := newOutfitService(t)

	_, err := svc.Like(context.Background(), pemain, "otf_tidak_ada")
	requireAPIError(t, err, http.StatusNotFound, "outfit_not_found")
}

// View tidak digabung per pemain: membuka outfit yang sama tiga kali memang
// tiga sinyal, dan itu bagian dari data yang mau dipakai melatih generator.
func TestRecordViewMenambahTiapPanggilan(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		result, err := svc.RecordView(ctx, pemain, seededOutfit)
		if err != nil {
			t.Fatalf("RecordView() ke-%d error = %v", i, err)
		}
		if result.ViewCount != i {
			t.Fatalf("viewCount = %d pada panggilan ke-%d, ingin %d", result.ViewCount, i, i)
		}
	}
}

func TestRecordViewAnonimTetapTercatat(t *testing.T) {
	svc := newOutfitService(t)

	result, err := svc.RecordView(context.Background(), anonim, seededOutfit)
	if err != nil {
		t.Fatalf("RecordView() error = %v", err)
	}
	if result.ViewCount != 1 {
		t.Errorf("viewCount = %d, ingin 1", result.ViewCount)
	}
}

func TestLikedByMenandaiHanyaYangDisukaiAktor(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	if _, err := svc.Like(ctx, pemain, seededOutfit); err != nil {
		t.Fatalf("Like() error = %v", err)
	}

	page, err := svc.List(ctx, service.ListOutfitFilter{}, "", 50)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	liked, err := svc.LikedBy(ctx, pemain, page.Outfits)
	if err != nil {
		t.Fatalf("LikedBy() error = %v", err)
	}
	if !liked[seededOutfit] {
		t.Errorf("outfit yang disukai tidak tertandai: %v", liked)
	}
	for id, ok := range liked {
		if id != seededOutfit && ok {
			t.Errorf("outfit %s tertandai disukai padahal tidak", id)
		}
	}

	// Aktor anonim tidak punya penanda sama sekali — peta nil, bukan peta
	// berisi false, supaya handler bisa membedakan "tidak suka" dari "tidak
	// tahu".
	anon, err := svc.LikedBy(ctx, anonim, page.Outfits)
	if err != nil {
		t.Fatalf("LikedBy() anonim error = %v", err)
	}
	if anon != nil {
		t.Errorf("LikedBy() anonim = %v, ingin nil", anon)
	}
}

func TestListMostLikedMengurutkanDariTerbanyak(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	base, err := svc.List(ctx, service.ListOutfitFilter{}, "", 50)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(base.Outfits) < 2 {
		t.Skipf("butuh minimal dua outfit di data contoh, ada %d", len(base.Outfits))
	}

	// Outfit terakhir pada urutan terbaru diberi dua like; setelah itu dia yang
	// harus memimpin urutan populer.
	favorit := base.Outfits[len(base.Outfits)-1].OutfitID
	if _, err := svc.Like(ctx, pemain, favorit); err != nil {
		t.Fatalf("Like() error = %v", err)
	}
	if _, err := svc.Like(ctx, pemain2, favorit); err != nil {
		t.Fatalf("Like() error = %v", err)
	}

	page, err := svc.List(ctx, service.ListOutfitFilter{Sort: "mostLiked"}, "", 50)
	if err != nil {
		t.Fatalf("List(mostLiked) error = %v", err)
	}
	if page.Outfits[0].OutfitID != favorit {
		t.Errorf("puncak mostLiked = %s, ingin %s", page.Outfits[0].OutfitID, favorit)
	}
	if page.Outfits[0].LikeCount != 2 {
		t.Errorf("likeCount puncak = %d, ingin 2", page.Outfits[0].LikeCount)
	}
}

func TestListMostViewedMengurutkanDariTerbanyak(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	base, err := svc.List(ctx, service.ListOutfitFilter{}, "", 50)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(base.Outfits) < 2 {
		t.Skipf("butuh minimal dua outfit di data contoh, ada %d", len(base.Outfits))
	}

	populer := base.Outfits[len(base.Outfits)-1].OutfitID
	for i := 0; i < 3; i++ {
		if _, err := svc.RecordView(ctx, pemain, populer); err != nil {
			t.Fatalf("RecordView() error = %v", err)
		}
	}

	page, err := svc.List(ctx, service.ListOutfitFilter{Sort: "mostViewed"}, "", 50)
	if err != nil {
		t.Fatalf("List(mostViewed) error = %v", err)
	}
	if page.Outfits[0].OutfitID != populer {
		t.Errorf("puncak mostViewed = %s, ingin %s", page.Outfits[0].OutfitID, populer)
	}
}

func TestListSortTidakDikenalDitolak(t *testing.T) {
	svc := newOutfitService(t)

	_, err := svc.List(context.Background(), service.ListOutfitFilter{Sort: "terpopuler"}, "", 10)
	requireAPIError(t, err, http.StatusBadRequest, "invalid_sort")
}

// Cursor menyimpan kunci paginasi yang hanya berlaku untuk satu urutan.
// Memakainya pada urutan lain akan menyaring baris dengan kunci yang salah,
// jadi harus ditolak terang-terangan, bukan diam-diam mengembalikan halaman
// ngawur.
func TestCursorDariSortLainDitolak(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	first, err := svc.List(ctx, service.ListOutfitFilter{}, "", 1)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if first.NextCursor == "" {
		t.Skip("data contoh hanya berisi satu outfit, tidak ada cursor")
	}

	_, err = svc.List(ctx, service.ListOutfitFilter{Sort: "mostLiked"}, first.NextCursor, 1)
	requireAPIError(t, err, http.StatusBadRequest, "cursor_sort_mismatch")
}

func TestPaginasiMostLikedTidakMengulangBaris(t *testing.T) {
	svc := newOutfitService(t)
	ctx := context.Background()

	all, err := svc.List(ctx, service.ListOutfitFilter{Sort: "mostLiked"}, "", 50)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all.Outfits) < 2 {
		t.Skipf("butuh minimal dua outfit, ada %d", len(all.Outfits))
	}

	// Beri like tidak merata supaya urutannya bukan sekadar seri di angka nol.
	if _, err := svc.Like(ctx, pemain, all.Outfits[len(all.Outfits)-1].OutfitID); err != nil {
		t.Fatalf("Like() error = %v", err)
	}

	seen := make(map[string]bool)
	cursor := ""
	for page := 0; page < 10; page++ {
		got, err := svc.List(ctx, service.ListOutfitFilter{Sort: "mostLiked"}, cursor, 1)
		if err != nil {
			t.Fatalf("List() halaman %d error = %v", page, err)
		}
		for _, o := range got.Outfits {
			if seen[o.OutfitID] {
				t.Fatalf("outfit %s muncul dua kali saat menelusuri halaman", o.OutfitID)
			}
			seen[o.OutfitID] = true
		}
		if !got.HasMore {
			break
		}
		cursor = got.NextCursor
	}

	if len(seen) != len(all.Outfits) {
		t.Errorf("menelusuri per halaman menemukan %d outfit, ingin %d", len(seen), len(all.Outfits))
	}
}
