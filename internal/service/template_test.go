package service_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

func newTemplateService(t *testing.T) (*service.Templates, *store.MemoryTemplates) {
	t.Helper()

	outfits := store.NewMemoryOutfits()
	templates := store.NewMemoryTemplates()
	store.SeedData(templates, outfits)

	return service.NewTemplates(templates), templates
}

func TestParseTemplateIDMenolakYangBukanAssetID(t *testing.T) {
	tests := map[string]struct {
		raw      string
		wantCode string
	}{
		"slug lama":     {"male_2", "invalid_template_id"},
		"ada huruf":     {"884842887927a6", "invalid_template_id"},
		"nol di depan":  {"088484288792766", "invalid_template_id"},
		"ada spasi":     {"884 842", "invalid_template_id"},
		"nol":           {"0", "invalid_template_id"},
		"negatif":       {"-88484288792766", "invalid_template_id"},
		"kosong":        {"   ", "missing_template"},
		"desimal float": {"8.8e13", "invalid_template_id"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := service.ParseTemplateID(tc.raw)
			requireAPIError(t, err, http.StatusUnprocessableEntity, tc.wantCode)
		})
	}
}

func TestParseTemplateIDMenerimaAssetID(t *testing.T) {
	assetID, err := service.ParseTemplateID("  88484288792766  ")
	if err != nil {
		t.Fatalf("ParseTemplateID() error = %v", err)
	}
	if assetID != 88484288792766 {
		t.Errorf("assetId = %d, ingin 88484288792766", assetID)
	}
}

func TestRegisterTemplateBaruLaluUlang(t *testing.T) {
	svc, _ := newTemplateService(t)
	ctx := context.Background()

	tpl, created, err := svc.Register(ctx, service.RegisterTemplateInput{
		TemplateID: newRig,
		Name:       "Rig Cewek V2",
		Gender:     "F",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if !created {
		t.Error("created = false pada pendaftaran pertama")
	}
	if tpl.Name != "Rig Cewek V2" || tpl.Gender != "F" || tpl.SourceAssetID != 77771111222233 {
		t.Errorf("hasil register = %+v", tpl)
	}

	// Pendaftaran ulang tidak boleh menimpa nama yang sudah ada.
	again, created, err := svc.Register(ctx, service.RegisterTemplateInput{
		TemplateID: newRig,
		Name:       "Nama Lain",
		Gender:     "M",
	})
	if err != nil {
		t.Fatalf("Register() kedua error = %v", err)
	}
	if created {
		t.Error("created = true padahal rig sudah terdaftar")
	}
	if again.Name != "Rig Cewek V2" || again.Gender != "F" {
		t.Errorf("rig terdaftar tertimpa: %+v", again)
	}
}

func TestRegisterTemplateValidasi(t *testing.T) {
	tests := map[string]struct {
		in       service.RegisterTemplateInput
		wantCode string
	}{
		"id bukan angka": {service.RegisterTemplateInput{TemplateID: "male_2"}, "invalid_template_id"},
		"gender ngawur":  {service.RegisterTemplateInput{TemplateID: newRig, Gender: "X"}, "invalid_gender"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := newTemplateService(t)
			_, _, err := svc.Register(context.Background(), tc.in)
			requireAPIError(t, err, http.StatusUnprocessableEntity, tc.wantCode)
		})
	}
}

func TestRegisterTemplateTanpaGenderJadiTidakDiketahui(t *testing.T) {
	svc, _ := newTemplateService(t)

	tpl, _, err := svc.Register(context.Background(), service.RegisterTemplateInput{TemplateID: newRig})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if tpl.Gender != "?" {
		t.Errorf("gender = %q, ingin '?'", tpl.Gender)
	}
}

func TestUpdateTemplateMengisiNamaDanGender(t *testing.T) {
	svc, _ := newTemplateService(t)
	ctx := context.Background()

	name := "Dev Rig Cowok"
	gender := "M"
	updated, err := svc.Update(ctx, devRig, service.UpdateTemplateInput{Name: &name, Gender: &gender})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != name || updated.Gender != gender {
		t.Errorf("hasil update = %+v", updated)
	}
	if updated.TemplateID != devRig {
		t.Errorf("templateId berubah: %q", updated.TemplateID)
	}
}

func TestUpdateTemplateBelumTerdaftar(t *testing.T) {
	svc, _ := newTemplateService(t)
	name := "Entah"

	_, err := svc.Update(context.Background(), newRig, service.UpdateTemplateInput{Name: &name})
	requireAPIError(t, err, http.StatusNotFound, "template_not_found")
}

func TestGetTemplateBelumTerdaftar(t *testing.T) {
	svc, _ := newTemplateService(t)

	_, err := svc.Get(context.Background(), newRig)
	requireAPIError(t, err, http.StatusNotFound, "template_not_found")
}

func TestListTemplatesTerbaruDulu(t *testing.T) {
	svc, _ := newTemplateService(t)
	ctx := context.Background()

	if _, _, err := svc.Register(ctx, service.RegisterTemplateInput{TemplateID: newRig, Name: "Rig Baru"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	page, err := svc.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Templates) != 2 {
		t.Fatalf("jumlah rig = %d, ingin 2", len(page.Templates))
	}
	if page.Templates[0].TemplateID != newRig {
		t.Errorf("urutan salah: %q di depan, ingin rig terbaru", page.Templates[0].TemplateID)
	}
	if page.HasMore || page.NextCursor != "" {
		t.Errorf("hasMore=%v nextCursor=%q, ingin habis", page.HasMore, page.NextCursor)
	}
}

func TestListTemplatesBerhalaman(t *testing.T) {
	svc, _ := newTemplateService(t)
	ctx := context.Background()

	if _, _, err := svc.Register(ctx, service.RegisterTemplateInput{TemplateID: newRig}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	first, err := svc.List(ctx, "", 1)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(first.Templates) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("halaman pertama = %+v", first)
	}

	second, err := svc.List(ctx, first.NextCursor, 1)
	if err != nil {
		t.Fatalf("List() halaman kedua error = %v", err)
	}
	if len(second.Templates) != 1 {
		t.Fatalf("halaman kedua = %+v", second.Templates)
	}
	if second.Templates[0].TemplateID == first.Templates[0].TemplateID {
		t.Error("halaman kedua mengulang baris yang sama")
	}
}
