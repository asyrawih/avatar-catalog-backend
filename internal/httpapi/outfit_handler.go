package httpapi

import (
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

type outfitHandler struct {
	outfits *service.Outfits
}

// --- request body ---------------------------------------------------------

// outfitItemBody menerima bentuk item yang sama persis dengan yang dikeluarkan
// GET /v1/outfits/{outfitId}, sehingga hasil GET bisa dikirim balik apa adanya.
//
// name, assetType, dan price disimpan apa adanya di OUTFIT_ITEM; ketiganya
// opsional karena assetId plus slot sudah cukup untuk memasang item.
type outfitItemBody struct {
	AssetID   int64  `json:"assetId"`
	Slot      string `json:"slot"`
	Name      string `json:"name"`
	AssetType string `json:"assetType"`
	Price     int    `json:"price"`
	// bundleId/bundleName opsional, terisi pada bagian paket. price bagian
	// paket adalah harga bundle induk yang terulang di tiap bagiannya.
	BundleID   int64  `json:"bundleId"`
	BundleName string `json:"bundleName"`
	// adjust opsional: koreksi penempatan asset pada rig. Tiap komponennya
	// berdiri sendiri dan boleh null — null berarti "biarkan bawaannya",
	// bukan nol.
	Adjust *itemAdjustBody `json:"adjust"`
}

type itemAdjustBody struct {
	Pos   *vec3Body `json:"pos"`
	Rot   *vec3Body `json:"rot"`
	Scale *vec3Body `json:"scale"`
}

type vec3Body struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// avatarBodyBody menerima bentuk body yang sama persis dengan yang dikeluarkan
// GET /v1/outfits/{outfitId}, jadi hasil GET bisa dikirim balik apa adanya.
//
// colors dan scales dua-duanya opsional; body yang tidak dikirim sama sekali
// berarti klien tidak melaporkannya.
type avatarBodyBody struct {
	Colors *bodyColorsBody `json:"colors"`
	Scales *bodyScalesBody `json:"scales"`
}

type bodyColorsBody struct {
	Head     string `json:"head"`
	Torso    string `json:"torso"`
	LeftArm  string `json:"leftArm"`
	RightArm string `json:"rightArm"`
	LeftLeg  string `json:"leftLeg"`
	RightLeg string `json:"rightLeg"`
}

type bodyScalesBody struct {
	Height     float64 `json:"height"`
	Width      float64 `json:"width"`
	Head       float64 `json:"head"`
	Depth      float64 `json:"depth"`
	BodyType   float64 `json:"bodyType"`
	Proportion float64 `json:"proportion"`
}

type createOutfitBody struct {
	UserID int64 `json:"userId"`
	// templateId adalah Roblox asset id rig; boleh dikirim sebagai string
	// maupun angka.
	TemplateID jsonID           `json:"templateId"`
	Name       string           `json:"name"`
	IsPublic   bool             `json:"isPublic"`
	CustomTags []string         `json:"customTags"`
	Items      []outfitItemBody `json:"items"`
	Body       *avatarBodyBody  `json:"body"`
	// thumbnailAssetId opsional: asset id thumbnail outfit hasil upload game
	// server.
	ThumbnailAssetID int64 `json:"thumbnailAssetId"`
}

// batchCreateOutfitBody adalah muatan POST /v1/outfits:batch. Tiap elemen
// outfits berbentuk sama persis dengan body POST /v1/outfits, jadi klien yang
// sudah bisa membuat satu outfit tidak perlu menyusun ulang muatannya.
type batchCreateOutfitBody struct {
	Outfits []createOutfitBody `json:"outfits"`
}

type updateOutfitBody struct {
	Name       *string   `json:"name"`
	TemplateID *jsonID   `json:"templateId"`
	IsPublic   *bool     `json:"isPublic"`
	CustomTags *[]string `json:"customTags"`
	RecoItemID *string   `json:"recoItemId"`
}

type replaceItemsBody struct {
	Items []outfitItemBody `json:"items"`
}

type resolveBody struct {
	ReferenceIDs []string `json:"referenceIds"`
}

func toModelItems(items []outfitItemBody) []model.OutfitItem {
	out := make([]model.OutfitItem, 0, len(items))
	for _, item := range items {
		out = append(out, model.OutfitItem{
			AssetID:    item.AssetID,
			Slot:       item.Slot,
			Name:       item.Name,
			AssetType:  item.AssetType,
			Price:      item.Price,
			BundleID:   item.BundleID,
			BundleName: item.BundleName,
			Adjust:     toModelAdjust(item.Adjust),
		})
	}
	return out
}

func toModelAdjust(adjust *itemAdjustBody) *model.ItemAdjust {
	if adjust == nil {
		return nil
	}

	out := model.ItemAdjust{
		Pos:   toModelVec3(adjust.Pos),
		Rot:   toModelVec3(adjust.Rot),
		Scale: toModelVec3(adjust.Scale),
	}
	if out.Pos == nil && out.Rot == nil && out.Scale == nil {
		return nil
	}
	return &out
}

func toModelVec3(v *vec3Body) *model.Vec3 {
	if v == nil {
		return nil
	}
	return &model.Vec3{X: v.X, Y: v.Y, Z: v.Z}
}

func toModelBody(body *avatarBodyBody) *model.AvatarBody {
	if body == nil {
		return nil
	}

	out := model.AvatarBody{}
	if c := body.Colors; c != nil {
		out.Colors = &model.BodyColors{
			Head:     c.Head,
			Torso:    c.Torso,
			LeftArm:  c.LeftArm,
			RightArm: c.RightArm,
			LeftLeg:  c.LeftLeg,
			RightLeg: c.RightLeg,
		}
	}
	if s := body.Scales; s != nil {
		out.Scales = &model.BodyScales{
			Height:     s.Height,
			Width:      s.Width,
			Head:       s.Head,
			Depth:      s.Depth,
			BodyType:   s.BodyType,
			Proportion: s.Proportion,
		}
	}
	return &out
}

// --- handler --------------------------------------------------------------

// list menangani GET /v1/outfits.
//
// userId opsional — tanpa userId daftar mencakup semua pemain. isPublic bisa
// dipakai untuk membatasi daftar gabungan itu ke outfit yang memang publik.
//
// q mencari sebagian nama outfit tanpa peduli huruf besar-kecil, sedangkan
// outfitId (boleh diulang atau dipisah koma) mengambil beberapa outfit
// sekaligus. Keduanya bisa dipadukan dengan userId dan isPublic.
func (h *outfitHandler) list(w http.ResponseWriter, r *http.Request) {
	userID, err := queryInt64(r, "userId")
	if err != nil {
		writeError(w, err)
		return
	}
	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	isPublic, err := queryBool(r, "isPublic")
	if err != nil {
		writeError(w, err)
		return
	}

	filter := service.ListOutfitFilter{
		UserID:    userID,
		IsPublic:  isPublic,
		OutfitIDs: queryList(r, "outfitId"),
		Keyword:   r.URL.Query().Get("q"),
		Sort:      r.URL.Query().Get("sort"),
	}
	page, err := h.outfits.List(r.Context(), filter, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, err)
		return
	}

	// Satu query untuk seluruh halaman, bukan satu per baris.
	liked, err := h.outfits.LikedBy(r.Context(), actorFrom(r.Context()), page.Outfits)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newListEnvelope(newOutfitSummaries(page.Outfits, liked), page.NextCursor, page.HasMore))
}

// like menangani POST /v1/outfits/{outfitId}/likes.
//
// Idempoten: menekan tombol suka dua kali berakhir pada keadaan yang sama dan
// tetap 200, dengan changed:false sebagai penanda bahwa yang kedua tidak
// mengubah apa pun.
func (h *outfitHandler) like(w http.ResponseWriter, r *http.Request) {
	result, err := h.outfits.Like(r.Context(), actorFrom(r.Context()), r.PathValue("outfitId"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newEngagement(result))
}

// unlike menangani DELETE /v1/outfits/{outfitId}/likes.
func (h *outfitHandler) unlike(w http.ResponseWriter, r *http.Request) {
	result, err := h.outfits.Unlike(r.Context(), actorFrom(r.Context()), r.PathValue("outfitId"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newEngagement(result))
}

// recordView menangani POST /v1/outfits/{outfitId}/views.
//
// Sengaja endpoint sendiri, bukan efek samping GET detail: GET tetap murni
// baca sehingga aman di-cache dan diulang, dan klien yang menentukan kapan
// sebuah outfit benar-benar terlihat — bukan setiap crawler yang kebetulan
// mengambilnya.
func (h *outfitHandler) recordView(w http.ResponseWriter, r *http.Request) {
	result, err := h.outfits.RecordView(r.Context(), actorFrom(r.Context()), r.PathValue("outfitId"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newEngagement(result))
}

// search menangani GET /v1/outfits/search.
//
// Beda dengan q pada GET /v1/outfits yang mencocokkan sebagian nama apa
// adanya, endpoint ini toleran salah ketik ("zepeto" menemukan "Aiche
// ZAPPETO") dan mengurutkan hasil dari yang paling mirip. Hasilnya peringkat,
// bukan halaman, jadi tidak ada cursor.
func (h *outfitHandler) search(w http.ResponseWriter, r *http.Request) {
	userID, err := queryInt64(r, "userId")
	if err != nil {
		writeError(w, err)
		return
	}
	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	isPublic, err := queryBool(r, "isPublic")
	if err != nil {
		writeError(w, err)
		return
	}

	rows, err := h.outfits.Search(r.Context(), service.SearchOutfitFilter{
		Query:    r.URL.Query().Get("q"),
		UserID:   userID,
		IsPublic: isPublic,
	}, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	liked, err := h.outfits.LikedBy(r.Context(), actorFrom(r.Context()), rows)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newListEnvelope(newOutfitSummaries(rows, liked), "", false))
}

// get menangani GET /v1/outfits/{outfitId}.
func (h *outfitHandler) get(w http.ResponseWriter, r *http.Request) {
	detail, err := h.outfits.Get(r.Context(), r.PathValue("outfitId"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newOutfitDetail(detail))
}

// create menangani POST /v1/outfits.
func (h *outfitHandler) create(w http.ResponseWriter, r *http.Request) {
	var body createOutfitBody
	if !decodeJSON(w, r, &body) {
		return
	}

	outfit, err := h.outfits.Create(r.Context(), service.CreateOutfitInput{
		UserID:           body.UserID,
		TemplateID:       body.TemplateID.String(),
		Name:             body.Name,
		IsPublic:         body.IsPublic,
		CustomTags:       body.CustomTags,
		Items:            toModelItems(body.Items),
		Body:             toModelBody(body.Body),
		ThumbnailAssetID: body.ThumbnailAssetID,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Location", "/v1/outfits/"+outfit.OutfitID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"outfitId":    outfit.OutfitID,
		"referenceId": outfit.ReferenceID,
		"recoItemId":  nullableString(outfit.RecoItemID),
		"createdAt":   outfit.CreatedAt,
	})
}

// createBatch menangani POST /v1/outfits:batch.
//
// Satu permintaan membawa banyak outfit sekaligus. Yang dihemat bukan hanya
// jumlah permintaan HTTP-nya: seluruh batch ditulis dalam satu transaksi dan
// satu perjalanan ke Postgres, jadi impor puluhan outfit tidak lagi membayar
// latensi per baris.
//
// Muatan yang cacat tidak menjatuhkan batch — tiap outfit punya hasil sendiri
// di results, ditandai index sesuai urutan kiriman. Statusnya mengikuti hasil
// itu: 201 kalau semuanya tersimpan, 200 kalau sebagian, dan 422 kalau tidak
// ada satu pun yang lolos.
func (h *outfitHandler) createBatch(w http.ResponseWriter, r *http.Request) {
	var body batchCreateOutfitBody
	if !decodeJSON(w, r, &body) {
		return
	}

	inputs := make([]service.CreateOutfitInput, 0, len(body.Outfits))
	for _, o := range body.Outfits {
		inputs = append(inputs, service.CreateOutfitInput{
			UserID:           o.UserID,
			TemplateID:       o.TemplateID.String(),
			Name:             o.Name,
			IsPublic:         o.IsPublic,
			CustomTags:       o.CustomTags,
			Items:            toModelItems(o.Items),
			Body:             toModelBody(o.Body),
			ThumbnailAssetID: o.ThumbnailAssetID,
		})
	}

	results, err := h.outfits.CreateBatch(r.Context(), inputs)
	if err != nil {
		writeError(w, err)
		return
	}

	envelope := newBatchCreateEnvelope(results)
	writeJSON(w, batchStatus(envelope), envelope)
}

// update menangani PATCH /v1/outfits/{outfitId}.
func (h *outfitHandler) update(w http.ResponseWriter, r *http.Request) {
	var body updateOutfitBody
	if !decodeJSON(w, r, &body) {
		return
	}

	var templateID *string
	if body.TemplateID != nil {
		id := body.TemplateID.String()
		templateID = &id
	}

	outfit, err := h.outfits.Update(r.Context(), actorFrom(r.Context()), r.PathValue("outfitId"), service.UpdateOutfitInput{
		Name:       body.Name,
		TemplateID: templateID,
		IsPublic:   body.IsPublic,
		CustomTags: body.CustomTags,
		RecoItemID: body.RecoItemID,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"outfitId":   outfit.OutfitID,
		"name":       outfit.Name,
		"isPublic":   outfit.IsPublic,
		"customTags": outfit.CustomTags,
		"recoItemId": nullableString(outfit.RecoItemID),
		"updatedAt":  outfit.UpdatedAt,
	})
}

// replaceItems menangani PUT /v1/outfits/{outfitId}/items.
func (h *outfitHandler) replaceItems(w http.ResponseWriter, r *http.Request) {
	var body replaceItemsBody
	if !decodeJSON(w, r, &body) {
		return
	}

	outfit, err := h.outfits.ReplaceItems(r.Context(), actorFrom(r.Context()), r.PathValue("outfitId"), toModelItems(body.Items))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"outfitId":  outfit.OutfitID,
		"itemCount": len(outfit.Items),
		"replaced":  true,
		"updatedAt": outfit.UpdatedAt,
	})
}

// remove menangani DELETE /v1/outfits/{outfitId} sebagai soft delete.
func (h *outfitHandler) remove(w http.ResponseWriter, r *http.Request) {
	outfit, err := h.outfits.SoftDelete(r.Context(), actorFrom(r.Context()), r.PathValue("outfitId"))
	if err != nil {
		writeError(w, err)
		return
	}

	deletedAt := time.Time{}
	if outfit.DeletedAt != nil {
		deletedAt = *outfit.DeletedAt
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"outfitId":   outfit.OutfitID,
		"deletedAt":  deletedAt,
		"recoItemId": nullableString(outfit.RecoItemID),
		"reminder":   "panggil RemoveItemAsync dengan recoItemId ini",
	})
}

// resolve menangani POST /v1/outfits/resolve.
//
// limit dan cursor dibaca dari query string, sama seperti GET /v1/outfits,
// supaya body permintaan tetap murni daftar id — klien cukup mengirim ulang
// body yang sama di tiap halaman dan hanya menukar URL-nya.
func (h *outfitHandler) resolve(w http.ResponseWriter, r *http.Request) {
	var body resolveBody
	if !decodeJSON(w, r, &body) {
		return
	}

	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}

	page, err := h.outfits.Resolve(r.Context(), body.ReferenceIDs, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, err)
		return
	}

	liked, err := h.outfits.LikedBy(r.Context(), actorFrom(r.Context()), page.Found)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resolveEnvelope{
		listEnvelope: newListEnvelope(newOutfitSummaries(page.Found, liked), page.NextCursor, page.HasMore),
		Total:        page.Total,
		TotalPages:   page.TotalPages,
		NotFound:     page.NotFound,
	})
}
