package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Batas yang dipakai endpoint outfit.
const (
	MaxResolveIDs   = 100
	MaxOutfitItems  = 30
	MaxCustomTags   = 20
	DefaultPageSize = 20
	MaxPageSize     = 100

	// MaxKeywordLen membatasi kata kunci pencarian nama outfit. Nama outfit
	// sendiri dibatasi lebih pendek, jadi kata kunci yang lebih panjang dari
	// ini pasti tidak cocok dengan apa pun.
	MaxKeywordLen = 120

	// MaxOutfitIDs membatasi jumlah outfitId yang boleh diminta sekaligus,
	// sejalan dengan batas resolve.
	MaxOutfitIDs = 100

	// MaxItemNameLen membatasi nama item yang dilaporkan klien.
	MaxItemNameLen = 120

	// MaxAssetTypeLen membatasi assetType. Nilainya berasal dari enum AssetType
	// Roblox ("Accessory", "Shirt", ...), jadi batas ini cuma pagar terhadap
	// muatan ngawur, bukan daftar putih — enum itu bertambah tanpa kabar.
	MaxAssetTypeLen = 40
)

// Actor adalah pemanggil di balik sebuah request.
//
// Untuk sekarang isinya berasal dari header dan belum diverifikasi — lihat
// httpapi.Authenticator. Begitu autentikasi asli dipasang, UserID datang dari
// token dan pemeriksaan kepemilikan di bawah langsung berlaku tanpa diubah.
type Actor struct {
	UserID  int64
	Present bool
}

// OutfitPage adalah satu halaman daftar outfit.
type OutfitPage struct {
	Outfits    []model.Outfit
	NextCursor string
	HasMore    bool
}

// CreateOutfitInput adalah muatan POST /v1/outfits.
type CreateOutfitInput struct {
	UserID     int64
	TemplateID string
	Name       string
	IsPublic   bool
	CustomTags []string
	Items      []model.OutfitItem
	Body       *model.AvatarBody // opsional
}

// UpdateOutfitInput adalah muatan PATCH /v1/outfits/{outfitId}; field nil
// dibiarkan apa adanya.
type UpdateOutfitInput struct {
	Name       *string
	IsPublic   *bool
	CustomTags *[]string
	RecoItemID *string
	TemplateID *string
}

// Outfits menyediakan operasi outfit pemain.
type Outfits struct {
	outfits   store.Outfits
	templates store.Templates
	now       func() time.Time
	newID     func() string
	newRefID  func() string
}

// NewOutfits merangkai service outfit.
func NewOutfits(outfits store.Outfits, templates store.Templates) *Outfits {
	return &Outfits{
		outfits:   outfits,
		templates: templates,
		now:       func() time.Time { return time.Now().UTC() },
		newID:     newOutfitID,
		newRefID:  newUUID,
	}
}

// ListOutfitFilter menyaring daftar outfit dari sisi API.
type ListOutfitFilter struct {
	UserID    int64    // 0 = semua pemain
	IsPublic  *bool    // nil = publik dan privat
	OutfitIDs []string // kosong = semua outfit
	Keyword   string   // kosong = tanpa pencarian nama
}

// List mengembalikan outfit, terbaru dulu.
//
// userId opsional: tanpa userId daftar mencakup semua pemain, yang berguna
// untuk feed dan untuk memeriksa isi katalog saat pengembangan.
func (s *Outfits) List(ctx context.Context, f ListOutfitFilter, rawCursor string, limit int) (OutfitPage, error) {
	if f.UserID < 0 {
		return OutfitPage{}, apierr.BadRequest("invalid_user_id", "Parameter userId tidak valid")
	}

	keyword := strings.TrimSpace(f.Keyword)
	if len([]rune(keyword)) > MaxKeywordLen {
		return OutfitPage{}, apierr.BadRequest("invalid_keyword", fmt.Sprintf("Kata kunci maksimal %d karakter", MaxKeywordLen))
	}
	if len(f.OutfitIDs) > MaxOutfitIDs {
		return OutfitPage{}, apierr.BadRequest("too_many_outfit_ids", fmt.Sprintf("Maksimal %d outfitId per permintaan", MaxOutfitIDs))
	}

	var cursor paging.KeysetCursor
	present, err := paging.Decode(rawCursor, &cursor)
	if err != nil {
		return OutfitPage{}, apierr.BadRequest("invalid_cursor", "Cursor tidak bisa dibaca")
	}
	after := &cursor
	if !present {
		after = nil
	}

	limit = paging.ClampLimit(limit, DefaultPageSize, MaxPageSize)
	rows, hasMore, err := s.outfits.List(ctx, store.OutfitFilter{
		UserID:    f.UserID,
		IsPublic:  f.IsPublic,
		OutfitIDs: f.OutfitIDs,
		Keyword:   keyword,
	}, after, limit)
	if err != nil {
		return OutfitPage{}, err
	}

	page := OutfitPage{Outfits: rows, HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = paging.Encode(paging.KeysetCursor{At: last.UpdatedAt, ID: last.OutfitID})
		if err != nil {
			return OutfitPage{}, err
		}
	}
	return page, nil
}

// Get mengembalikan satu outfit lengkap dengan itemnya.
func (s *Outfits) Get(ctx context.Context, outfitID string) (model.Outfit, error) {
	return s.load(ctx, outfitID)
}

// Create membuat outfit baru. Backend yang membangkitkan referenceId, karena
// nilai itulah yang dikirim ke RegisterItemAsync — bukan outfitId internal.
func (s *Outfits) Create(ctx context.Context, in CreateOutfitInput) (model.Outfit, error) {
	if in.UserID <= 0 {
		return model.Outfit{}, apierr.Unprocessable("missing_user_id", "Field userId wajib diisi")
	}
	if strings.TrimSpace(in.Name) == "" {
		return model.Outfit{}, apierr.Unprocessable("missing_name", "Field name wajib diisi")
	}
	if err := s.ensureTemplate(ctx, in.TemplateID); err != nil {
		return model.Outfit{}, err
	}
	if err := validateCustomTags(in.CustomTags); err != nil {
		return model.Outfit{}, err
	}
	if err := s.validateItems(in.Items); err != nil {
		return model.Outfit{}, err
	}
	body, err := normalizeBody(in.Body)
	if err != nil {
		return model.Outfit{}, err
	}

	now := s.now()
	outfit := model.Outfit{
		OutfitID:    s.newID(),
		ReferenceID: s.newRefID(),
		UserID:      in.UserID,
		TemplateID:  strings.TrimSpace(in.TemplateID),
		Name:        strings.TrimSpace(in.Name),
		IsPublic:    in.IsPublic,
		CustomTags:  in.CustomTags,
		Items:       in.Items,
		Body:        body,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if outfit.CustomTags == nil {
		outfit.CustomTags = []string{}
	}
	if outfit.Items == nil {
		outfit.Items = []model.OutfitItem{}
	}

	if err := s.outfits.Create(ctx, outfit); err != nil {
		return model.Outfit{}, err
	}
	return outfit, nil
}

// Update mengubah sebagian metadata outfit, termasuk menyimpan recoItemId
// hasil RegisterItemAsync.
func (s *Outfits) Update(ctx context.Context, actor Actor, outfitID string, in UpdateOutfitInput) (model.Outfit, error) {
	current, err := s.load(ctx, outfitID)
	if err != nil {
		return model.Outfit{}, err
	}
	if err := ensureOwner(actor, current); err != nil {
		return model.Outfit{}, err
	}
	if in.CustomTags != nil {
		if err := validateCustomTags(*in.CustomTags); err != nil {
			return model.Outfit{}, err
		}
	}
	if in.TemplateID != nil {
		if err := s.ensureTemplate(ctx, *in.TemplateID); err != nil {
			return model.Outfit{}, err
		}
		trimmed := strings.TrimSpace(*in.TemplateID)
		in.TemplateID = &trimmed
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return model.Outfit{}, apierr.Unprocessable("missing_name", "Field name tidak boleh kosong")
	}

	return s.outfits.Update(ctx, outfitID, func(o *model.Outfit) error {
		if in.Name != nil {
			o.Name = strings.TrimSpace(*in.Name)
		}
		if in.IsPublic != nil {
			o.IsPublic = *in.IsPublic
		}
		if in.CustomTags != nil {
			o.CustomTags = *in.CustomTags
		}
		if in.RecoItemID != nil {
			recoItemID := *in.RecoItemID
			o.RecoItemID = &recoItemID
		}
		if in.TemplateID != nil {
			o.TemplateID = *in.TemplateID
		}
		o.UpdatedAt = s.now()
		return nil
	})
}

// ReplaceItems mengganti seluruh isi item outfit.
//
// Sengaja mengganti seluruh koleksi, bukan menambal sebagian: item outfit
// adalah himpunan, dan mengganti seluruhnya membuat operasi ini idempoten
// sehingga aman diulang saat retry.
func (s *Outfits) ReplaceItems(ctx context.Context, actor Actor, outfitID string, items []model.OutfitItem) (model.Outfit, error) {
	current, err := s.load(ctx, outfitID)
	if err != nil {
		return model.Outfit{}, err
	}
	if err := ensureOwner(actor, current); err != nil {
		return model.Outfit{}, err
	}
	if err := s.validateItems(items); err != nil {
		return model.Outfit{}, err
	}

	return s.outfits.Update(ctx, outfitID, func(o *model.Outfit) error {
		o.Items = items
		if o.Items == nil {
			o.Items = []model.OutfitItem{}
		}
		o.UpdatedAt = s.now()
		return nil
	})
}

// SoftDelete mengisi deletedAt tanpa menghapus baris.
//
// Setelah ini pemanggil wajib memanggil RecommendationService:RemoveItemAsync
// dengan recoItemId yang dikembalikan, kalau tidak outfit yang sudah dihapus
// tetap muncul di feed dan referenceId-nya menggantung.
func (s *Outfits) SoftDelete(ctx context.Context, actor Actor, outfitID string) (model.Outfit, error) {
	current, err := s.load(ctx, outfitID)
	if err != nil {
		return model.Outfit{}, err
	}
	if err := ensureOwner(actor, current); err != nil {
		return model.Outfit{}, err
	}

	now := s.now()
	return s.outfits.Update(ctx, outfitID, func(o *model.Outfit) error {
		o.DeletedAt = &now
		o.UpdatedAt = now
		return nil
	})
}

// Resolve menukar sekumpulan referenceId dari feed rekomendasi menjadi
// metadata render.
func (s *Outfits) Resolve(ctx context.Context, referenceIDs []string) ([]model.Outfit, []string, error) {
	if len(referenceIDs) == 0 {
		return []model.Outfit{}, []string{}, nil
	}
	if len(referenceIDs) > MaxResolveIDs {
		return nil, nil, apierr.TooLarge("too_many_ids", fmt.Sprintf("Maksimum %d referenceId per permintaan", MaxResolveIDs))
	}

	found, err := s.outfits.ListByReferenceIDs(ctx, referenceIDs)
	if err != nil {
		return nil, nil, err
	}

	seen := make(map[string]struct{}, len(found))
	for _, o := range found {
		seen[o.ReferenceID] = struct{}{}
	}

	notFound := make([]string, 0)
	for _, ref := range referenceIDs {
		if _, ok := seen[ref]; !ok {
			notFound = append(notFound, ref)
		}
	}
	return found, notFound, nil
}

// load mengambil outfit dan menerjemahkan keadaannya menjadi 404 atau 410.
func (s *Outfits) load(ctx context.Context, outfitID string) (model.Outfit, error) {
	if strings.TrimSpace(outfitID) == "" {
		return model.Outfit{}, apierr.NotFound("outfit_not_found", "Outfit tidak ditemukan")
	}

	outfit, err := s.outfits.Get(ctx, outfitID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Outfit{}, apierr.NotFound("outfit_not_found", fmt.Sprintf("Outfit %s tidak ditemukan", outfitID))
	}
	if err != nil {
		return model.Outfit{}, err
	}
	if outfit.Deleted() {
		return model.Outfit{}, apierr.Gone("outfit_deleted", fmt.Sprintf("Outfit dihapus pada %s", outfit.DeletedAt.Format(time.RFC3339)))
	}
	return outfit, nil
}

// ensureTemplate memastikan rig terdaftar di BODY_TEMPLATE, mendaftarkannya
// bila ini pemakaian pertama.
//
// Rig di-upload ke Roblox lebih dulu dan Roblox yang memegang asetnya, jadi
// menolak asset id yang belum pernah dilihat backend hanya akan menghalangi
// alur yang sah. Yang tetap ditolak adalah id yang bukan asset id sama sekali,
// supaya registry tidak terisi salah ketik dan FK OUTFIT.templateId tetap berarti.
func (s *Outfits) ensureTemplate(ctx context.Context, templateID string) error {
	assetID, err := ParseTemplateID(templateID)
	if err != nil {
		return err
	}
	templateID = strings.TrimSpace(templateID)

	if _, err := s.templates.Get(ctx, templateID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	// Nama dan gender dibiarkan kosong — backend tidak tahu keduanya sampai
	// diisi lewat PATCH /v1/templates/{templateId}.
	_, _, err = s.templates.Ensure(ctx, model.BodyTemplate{
		TemplateID:    templateID,
		Gender:        genderUnknown,
		SourceAssetID: assetID,
		CreatedAt:     s.now(),
	})
	return err
}

// validateItems memeriksa jumlah dan bentuk detail item.
//
// Yang tidak diperiksa: apakah assetId-nya benar-benar ada di Roblox. Backend
// tidak lagi menyimpan katalog, jadi satu-satunya yang tahu itu adalah klien —
// dan menebaknya di sini hanya akan menolak item yang sah.
//
// Slot ganda juga tidak ditolak. Klien yang tahu apakah dua asset benar-benar
// bentrok di rig; slot di sini cuma label yang dilaporkan klien.
func (s *Outfits) validateItems(items []model.OutfitItem) error {
	if len(items) > MaxOutfitItems {
		return apierr.Unprocessable("too_many_items", fmt.Sprintf("Maksimum %d item per outfit", MaxOutfitItems))
	}

	for _, item := range items {
		if strings.TrimSpace(item.Slot) == "" {
			return apierr.Unprocessable("missing_slot", "Setiap item wajib punya slot")
		}
		if item.AssetID <= 0 {
			return apierr.Unprocessable("invalid_asset_id", "Setiap item wajib punya assetId")
		}

		if len(strings.TrimSpace(item.Name)) > MaxItemNameLen {
			return apierr.Unprocessable("invalid_item_name",
				fmt.Sprintf("name item maksimum %d karakter", MaxItemNameLen))
		}
		if len(strings.TrimSpace(item.AssetType)) > MaxAssetTypeLen {
			return apierr.Unprocessable("invalid_asset_type",
				fmt.Sprintf("assetType maksimum %d karakter", MaxAssetTypeLen))
		}
		if item.Price < 0 {
			return apierr.Unprocessable("invalid_item_price", "price item tidak boleh negatif")
		}
	}
	return nil
}

// normalizeBody memeriksa warna dan skala tubuh, lalu mengembalikan salinan
// yang sudah dirapikan.
//
// Yang tidak diperiksa: apakah skalanya masuk rentang yang diterima Roblox.
// Rentang itu bergeser antar rig dan antar rilis, jadi menebaknya di sini hanya
// akan menolak avatar yang sah — sama alasannya dengan assetId item. Yang
// ditolak hanya nilai yang tidak mungkin benar dan pasti bikin render gagal.
func normalizeBody(body *model.AvatarBody) (*model.AvatarBody, error) {
	if body == nil {
		return nil, nil
	}

	out := model.AvatarBody{}

	if body.Colors != nil {
		colors := *body.Colors
		parts := []struct {
			field string
			value *string
		}{
			{"head", &colors.Head},
			{"torso", &colors.Torso},
			{"leftArm", &colors.LeftArm},
			{"rightArm", &colors.RightArm},
			{"leftLeg", &colors.LeftLeg},
			{"rightLeg", &colors.RightLeg},
		}
		for _, part := range parts {
			normalized, err := normalizeHexColor(*part.value)
			if err != nil {
				return nil, apierr.Unprocessable("invalid_body_color",
					fmt.Sprintf("body.colors.%s harus hex RGB 6 digit", part.field)).
					WithDetails(map[string]any{
						"field": "body.colors." + part.field,
						"value": *part.value,
					})
			}
			*part.value = normalized
		}
		out.Colors = &colors
	}

	if body.Scales != nil {
		scales := *body.Scales
		// Height/width/head/depth adalah pengali: nol berarti anggota badan
		// menghilang, dan itu selalu salah kirim — biasanya karena field-nya
		// lupa diisi, bukan karena benar-benar diinginkan.
		positive := []struct {
			field string
			value float64
		}{
			{"height", scales.Height},
			{"width", scales.Width},
			{"head", scales.Head},
			{"depth", scales.Depth},
		}
		for _, s := range positive {
			if s.value <= 0 {
				return nil, apierr.Unprocessable("invalid_body_scale",
					fmt.Sprintf("body.scales.%s harus lebih besar dari nol", s.field)).
					WithDetails(map[string]any{"field": "body.scales." + s.field, "value": s.value})
			}
		}
		// BodyType dan proportion adalah bobot; nol adalah nilai wajarnya.
		weights := []struct {
			field string
			value float64
		}{
			{"bodyType", scales.BodyType},
			{"proportion", scales.Proportion},
		}
		for _, s := range weights {
			if s.value < 0 {
				return nil, apierr.Unprocessable("invalid_body_scale",
					fmt.Sprintf("body.scales.%s tidak boleh negatif", s.field)).
					WithDetails(map[string]any{"field": "body.scales." + s.field, "value": s.value})
			}
		}
		out.Scales = &scales
	}

	// body: {} tidak membawa informasi apa pun; simpan sebagai tidak dilaporkan
	// supaya tidak ada dua bentuk berbeda untuk keadaan yang sama.
	if out.Colors == nil && out.Scales == nil {
		return nil, nil
	}
	return &out, nil
}

// normalizeHexColor menerima "AE7C64" maupun "#AE7C64" dan mengembalikannya
// tanpa '#'. Besar-kecil huruf dibiarkan seperti yang dikirim klien.
func normalizeHexColor(value string) (string, error) {
	hex := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(hex) != 6 {
		return "", errInvalidHex
	}
	for _, r := range hex {
		isDigit := r >= '0' && r <= '9'
		isHexLetter := (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isDigit && !isHexLetter {
			return "", errInvalidHex
		}
	}
	return hex, nil
}

var errInvalidHex = errors.New("bukan hex RGB 6 digit")

// validateCustomTags menolak tag yang mengandung koma, karena tag dikirim ke
// RegisterItemAsync sebagai satu string dipisah koma dan akan terpecah salah.
func validateCustomTags(tags []string) error {
	if len(tags) > MaxCustomTags {
		return apierr.Unprocessable("too_many_custom_tags", fmt.Sprintf("Maksimum %d customTags", MaxCustomTags))
	}

	invalid := apierr.Unprocessable("invalid_custom_tag", "CustomTags tidak boleh mengandung koma")
	for i, tag := range tags {
		if strings.Contains(tag, ",") {
			invalid.WithDetails(map[string]any{
				"field": fmt.Sprintf("customTags[%d]", i),
				"value": tag,
			})
		}
	}
	if len(invalid.Details) > 0 {
		return invalid
	}
	return nil
}

// ensureOwner menolak perubahan dari pemain lain. Selama autentikasi belum
// terpasang, actor tanpa identitas dilewatkan begitu saja.
func ensureOwner(actor Actor, outfit model.Outfit) error {
	if !actor.Present || actor.UserID == outfit.UserID {
		return nil
	}
	return apierr.Forbidden("not_owner", fmt.Sprintf("userId %d bukan pemilik outfit %s", actor.UserID, outfit.OutfitID))
}
