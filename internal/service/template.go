package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// maxTemplateNameLen membatasi panjang nama rig.
const maxTemplateNameLen = 120

// genderUnknown dipakai untuk rig yang didaftarkan otomatis — backend tidak
// tahu gendernya sampai seseorang mengisinya lewat PATCH.
const genderUnknown = "?"

// RegisterTemplateInput adalah muatan POST /v1/templates.
type RegisterTemplateInput struct {
	TemplateID string
	Name       string
	Gender     string
}

// UpdateTemplateInput adalah muatan PATCH /v1/templates/{templateId}.
type UpdateTemplateInput struct {
	Name   *string
	Gender *string
}

// TemplatePage adalah satu halaman daftar rig.
type TemplatePage struct {
	Templates  []model.BodyTemplate
	NextCursor string
	HasMore    bool
}

// Templates mengelola registry rig yang sudah di-upload ke Roblox.
//
// Roblox yang memegang rig-nya; tabel BODY_TEMPLATE di sini hanya mencatat rig
// mana saja yang dipakai outfit, supaya foreign key OUTFIT.templateId tetap
// berarti dan daftar rig bisa ditelusuri klien.
type Templates struct {
	templates store.Templates
	now       func() time.Time
}

// NewTemplates merangkai service registry rig.
func NewTemplates(templates store.Templates) *Templates {
	return &Templates{
		templates: templates,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// Register mendaftarkan rig secara eksplisit, lengkap dengan nama dan gender.
// Nilai kedua menandai apakah baris baru benar-benar dibuat.
func (s *Templates) Register(ctx context.Context, in RegisterTemplateInput) (model.BodyTemplate, bool, error) {
	assetID, err := ParseTemplateID(in.TemplateID)
	if err != nil {
		return model.BodyTemplate{}, false, err
	}

	name := strings.TrimSpace(in.Name)
	if len(name) > maxTemplateNameLen {
		return model.BodyTemplate{}, false, apierr.Unprocessable("invalid_name", fmt.Sprintf("name maksimum %d karakter", maxTemplateNameLen))
	}

	gender := strings.TrimSpace(in.Gender)
	if gender == "" {
		gender = genderUnknown
	}
	if err := validateGender(gender); err != nil {
		return model.BodyTemplate{}, false, err
	}

	saved, created, err := s.templates.Ensure(ctx, model.BodyTemplate{
		TemplateID:    strings.TrimSpace(in.TemplateID),
		Name:          name,
		Gender:        gender,
		SourceAssetID: assetID,
		CreatedAt:     s.now(),
	})
	if err != nil {
		return model.BodyTemplate{}, false, err
	}
	return saved, created, nil
}

// Get mengembalikan satu rig.
func (s *Templates) Get(ctx context.Context, templateID string) (model.BodyTemplate, error) {
	if _, err := ParseTemplateID(templateID); err != nil {
		return model.BodyTemplate{}, err
	}

	tpl, err := s.templates.Get(ctx, strings.TrimSpace(templateID))
	if errors.Is(err, store.ErrNotFound) {
		return model.BodyTemplate{}, apierr.NotFound("template_not_found", fmt.Sprintf("Rig %s belum terdaftar", templateID))
	}
	return tpl, err
}

// List mengembalikan rig terdaftar, terbaru dulu.
func (s *Templates) List(ctx context.Context, rawCursor string, limit int) (TemplatePage, error) {
	var cursor paging.KeysetCursor
	present, err := paging.Decode(rawCursor, &cursor)
	if err != nil {
		return TemplatePage{}, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
	}
	after := &cursor
	if !present {
		after = nil
	}

	limit = paging.ClampLimit(limit, DefaultPageSize, MaxPageSize)
	rows, hasMore, err := s.templates.List(ctx, after, limit)
	if err != nil {
		return TemplatePage{}, err
	}

	page := TemplatePage{Templates: rows, HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = paging.Encode(paging.KeysetCursor{At: last.CreatedAt, ID: last.TemplateID})
		if err != nil {
			return TemplatePage{}, err
		}
	}
	return page, nil
}

// Update mengisi nama dan gender rig yang sudah terdaftar.
func (s *Templates) Update(ctx context.Context, templateID string, in UpdateTemplateInput) (model.BodyTemplate, error) {
	if _, err := ParseTemplateID(templateID); err != nil {
		return model.BodyTemplate{}, err
	}

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if len(name) > maxTemplateNameLen {
			return model.BodyTemplate{}, apierr.Unprocessable("invalid_name", fmt.Sprintf("name maksimum %d karakter", maxTemplateNameLen))
		}
		in.Name = &name
	}
	if in.Gender != nil {
		gender := strings.TrimSpace(*in.Gender)
		if err := validateGender(gender); err != nil {
			return model.BodyTemplate{}, err
		}
		in.Gender = &gender
	}

	updated, err := s.templates.Update(ctx, strings.TrimSpace(templateID), func(t *model.BodyTemplate) error {
		if in.Name != nil {
			t.Name = *in.Name
		}
		if in.Gender != nil {
			t.Gender = *in.Gender
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return model.BodyTemplate{}, apierr.NotFound("template_not_found", fmt.Sprintf("Rig %s belum terdaftar", templateID))
	}
	return updated, err
}

// ParseTemplateID memvalidasi bahwa templateId berbentuk Roblox asset id.
//
// Rig di-upload ke Roblox lebih dulu, jadi satu-satunya bentuk id yang sah
// adalah asset id hasil upload itu: digit saja, tanpa nol di depan. Menolak
// bentuk lain menjaga registry tidak terisi salah ketik.
func ParseTemplateID(raw string) (int64, error) {
	templateID := strings.TrimSpace(raw)
	if templateID == "" {
		return 0, apierr.Unprocessable("missing_template", "Field templateId wajib diisi")
	}

	invalid := apierr.Unprocessable("invalid_template_id",
		fmt.Sprintf("templateId %q harus berupa Roblox asset id (angka saja)", templateID))

	for _, r := range templateID {
		if r < '0' || r > '9' {
			return 0, invalid
		}
	}
	if len(templateID) > 1 && templateID[0] == '0' {
		return 0, invalid
	}

	assetID, err := strconv.ParseInt(templateID, 10, 64)
	if err != nil || assetID <= 0 {
		return 0, invalid
	}
	return assetID, nil
}

func validateGender(gender string) error {
	switch gender {
	case "M", "F", genderUnknown:
		return nil
	default:
		return apierr.Unprocessable("invalid_gender", "gender harus salah satu dari M, F, ?")
	}
}
