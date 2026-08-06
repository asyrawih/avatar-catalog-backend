package service_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/service"
)

// TestSearchToleranSalahKetik memastikan salah ketik nama tetap menemukan
// outfit-nya — inti dari pencarian trigram.
func TestSearchToleranSalahKetik(t *testing.T) {
	svc := newOutfitService(t)

	created, err := svc.Create(context.Background(), service.CreateOutfitInput{
		UserID:     seededUser,
		TemplateID: devRig,
		Name:       "Aiche ZAPPETO",
		IsPublic:   true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	for _, q := range []string{"zappeto", "zepeto", "zeppeto", "aiche"} {
		rows, err := svc.Search(context.Background(), service.SearchOutfitFilter{Query: q}, 10)
		if err != nil {
			t.Fatalf("Search(%q) error = %v", q, err)
		}

		found := false
		for _, o := range rows {
			if o.OutfitID == created.OutfitID {
				found = true
			}
		}
		if !found {
			t.Errorf("Search(%q) tidak menemukan %q", q, created.Name)
		}
	}
}

// TestSearchMenyaringDanMemeringkat memastikan filter ikut bekerja dan nama
// yang lebih mirip muncul lebih dulu.
func TestSearchMenyaringDanMemeringkat(t *testing.T) {
	svc := newOutfitService(t)

	mk := func(name string, isPublic bool) string {
		t.Helper()
		created, err := svc.Create(context.Background(), service.CreateOutfitInput{
			UserID: seededUser, TemplateID: devRig, Name: name, IsPublic: isPublic,
		})
		if err != nil {
			t.Fatalf("Create(%q) error = %v", name, err)
		}
		return created.OutfitID
	}

	exact := mk("Zappeto Classic", true)
	mk("Zappet Deluxe", true) // pengisi peringkat: mirip tapi tidak persis
	private := mk("Zappeto Privat", false)

	isPublic := true
	rows, err := svc.Search(context.Background(), service.SearchOutfitFilter{
		Query: "zappeto", IsPublic: &isPublic,
	}, 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("Search() kosong; mestinya menemukan outfit zappeto")
	}
	if rows[0].OutfitID != exact {
		t.Errorf("hasil pertama = %s, mestinya kecocokan persis %s", rows[0].OutfitID, exact)
	}
	for _, o := range rows {
		if o.OutfitID == private {
			t.Errorf("outfit privat %s ikut muncul padahal isPublic=true", private)
		}
	}
}

// TestSearchMenolakQueryTidakValid memastikan validasi parameter q.
func TestSearchMenolakQueryTidakValid(t *testing.T) {
	svc := newOutfitService(t)

	for _, q := range []string{"", " ", "z"} {
		_, err := svc.Search(context.Background(), service.SearchOutfitFilter{Query: q}, 10)
		requireAPIError(t, err, http.StatusBadRequest, "invalid_query")
	}
}
