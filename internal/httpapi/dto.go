package httpapi

import (
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

// listEnvelope adalah bentuk seragam untuk semua daftar berhalaman.
type listEnvelope struct {
	Data       any     `json:"data"`
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

func newListEnvelope(data any, nextCursor string, hasMore bool) listEnvelope {
	env := listEnvelope{Data: data, HasMore: hasMore}
	if nextCursor != "" {
		env.NextCursor = &nextCursor
	}
	return env
}

// resolveEnvelope adalah envelope daftar biasa ditambah referenceId yang tidak
// ketemu. notFound hanya memuat id dari halaman ini — id di halaman berikutnya
// belum pernah dicari.
// total dan totalPages menghitung referenceId yang dikirim klien, bukan isi
// database — angkanya pasti dan tidak memerlukan query hitung.
type resolveEnvelope struct {
	listEnvelope
	Total      int      `json:"total"`
	TotalPages int      `json:"totalPages"`
	NotFound   []string `json:"notFound"`
}

// --- batch create ---------------------------------------------------------

// batchCreateEnvelope adalah balasan POST /v1/outfits:batch.
//
// created dan failed dihitungkan di sini supaya importer tidak perlu menyapu
// results hanya untuk tahu apakah semuanya lolos.
type batchCreateEnvelope struct {
	Created int              `json:"created"`
	Failed  int              `json:"failed"`
	Results []batchResultDTO `json:"results"`
}

// batchResultDTO adalah nasib satu outfit di dalam batch. Persis satu di antara
// outfitId dan error yang terisi; index selalu ada dan menunjuk posisi di
// daftar yang klien kirim.
type batchResultDTO struct {
	Index       int        `json:"index"`
	OutfitID    string     `json:"outfitId,omitempty"`
	ReferenceID string     `json:"referenceId,omitempty"`
	RecoItemID  any        `json:"recoItemId,omitempty"`
	CreatedAt   *time.Time `json:"createdAt,omitempty"`
	Error       *errorBody `json:"error,omitempty"`
}

func newBatchCreateEnvelope(results []service.BatchOutfitResult) batchCreateEnvelope {
	env := batchCreateEnvelope{Results: make([]batchResultDTO, 0, len(results))}

	for _, r := range results {
		row := batchResultDTO{Index: r.Index}
		if r.Created() {
			createdAt := r.Outfit.CreatedAt
			row.OutfitID = r.Outfit.OutfitID
			row.ReferenceID = r.Outfit.ReferenceID
			row.RecoItemID = nullableString(r.Outfit.RecoItemID)
			row.CreatedAt = &createdAt
			env.Created++
		} else {
			row.Error = newErrorBody(r.Err)
			env.Failed++
		}
		env.Results = append(env.Results, row)
	}
	return env
}

// batchStatus menerjemahkan hasil batch menjadi status HTTP: 201 kalau semua
// tersimpan, 200 kalau sebagian, 422 kalau tidak ada yang lolos.
//
// Balasannya tetap berbentuk batchCreateEnvelope walau statusnya 422 — yang
// menjelaskan kegagalannya adalah error per-outfit di results, dan
// membungkusnya jadi satu error tunggal justru membuang keterangan itu.
func batchStatus(env batchCreateEnvelope) int {
	switch {
	case env.Created == 0:
		return http.StatusUnprocessableEntity
	case env.Failed == 0:
		return http.StatusCreated
	default:
		return http.StatusOK
	}
}

// --- outfit ---------------------------------------------------------------

// outfitSummaryDTO membawa itemnya sekalian. Penyimpanan sudah mengambil item
// satu halaman penuh dalam satu query, jadi menyertakannya di sini tidak
// menambah pukulan ke database — dan klien tidak perlu menyusul GET detail per
// outfit hanya untuk tahu isinya.
//
// itemCount dipertahankan supaya klien yang cuma butuh jumlah tidak ikut
// berubah.
type outfitSummaryDTO struct {
	OutfitID         string          `json:"outfitId"`
	ReferenceID      string          `json:"referenceId"`
	UserID           int64           `json:"userId"`
	Name             string          `json:"name"`
	TemplateID       string          `json:"templateId"`
	IsPublic         bool            `json:"isPublic"`
	ItemCount        int             `json:"itemCount"`
	Items            []outfitItemDTO `json:"items"`
	Body             *avatarBodyDTO  `json:"body"`
	ThumbnailAssetID int64           `json:"thumbnailAssetId,omitempty"`
	// likeCount dan viewCount adalah popularitas outfit. Angkanya bisa
	// tertinggal paling lama selama CACHE_TTL saat cache baca aktif — cukup
	// untuk ditampilkan, jangan dipakai sebagai sumber kebenaran akuntansi.
	LikeCount int `json:"likeCount"`
	ViewCount int `json:"viewCount"`
	// likedByMe hanya muncul untuk pemanggil yang dikenali; permintaan anonim
	// tidak membawanya sama sekali, bukan membawanya bernilai false.
	LikedByMe *bool     `json:"likedByMe,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func newOutfitSummary(o model.Outfit) outfitSummaryDTO {
	return outfitSummaryDTO{
		OutfitID:         o.OutfitID,
		ReferenceID:      o.ReferenceID,
		UserID:           o.UserID,
		Name:             o.Name,
		TemplateID:       o.TemplateID,
		IsPublic:         o.IsPublic,
		ItemCount:        len(o.Items),
		Items:            newOutfitItems(o.Items),
		Body:             newAvatarBody(o.Body),
		ThumbnailAssetID: o.ThumbnailAssetID,
		LikeCount:        o.LikeCount,
		ViewCount:        o.ViewCount,
		UpdatedAt:        o.UpdatedAt,
	}
}

// newOutfitSummaries menyusun ringkasan daftar. liked nil berarti pemanggil
// anonim, sehingga likedByMe tidak ikut muncul.
func newOutfitSummaries(outfits []model.Outfit, liked map[string]bool) []outfitSummaryDTO {
	out := make([]outfitSummaryDTO, 0, len(outfits))
	for _, o := range outfits {
		summary := newOutfitSummary(o)
		if liked != nil {
			byMe := liked[o.OutfitID]
			summary.LikedByMe = &byMe
		}
		out = append(out, summary)
	}
	return out
}

// engagementDTO adalah balasan POST/DELETE like dan POST view.
type engagementDTO struct {
	OutfitID string `json:"outfitId"`
	// changed false berarti permintaan tidak mengubah apa pun — mis. like
	// kedua dari pemain yang sama. Bukan kegagalan.
	Changed   bool `json:"changed"`
	Liked     bool `json:"liked"`
	LikeCount int  `json:"likeCount"`
	ViewCount int  `json:"viewCount"`
}

func newEngagement(e service.Engagement) engagementDTO {
	return engagementDTO{
		OutfitID:  e.OutfitID,
		Changed:   e.Changed,
		Liked:     e.Liked,
		LikeCount: e.LikeCount,
		ViewCount: e.ViewCount,
	}
}

// avatarBodyDTO adalah warna dan skala tubuh. Bentuknya sama dengan yang
// diterima POST /v1/outfits, jadi hasil GET bisa dikirim balik apa adanya.
type avatarBodyDTO struct {
	Colors *bodyColorsDTO `json:"colors"`
	Scales *bodyScalesDTO `json:"scales"`
}

type bodyColorsDTO struct {
	Head     string `json:"head"`
	Torso    string `json:"torso"`
	LeftArm  string `json:"leftArm"`
	RightArm string `json:"rightArm"`
	LeftLeg  string `json:"leftLeg"`
	RightLeg string `json:"rightLeg"`
}

type bodyScalesDTO struct {
	Height     float64 `json:"height"`
	Width      float64 `json:"width"`
	Head       float64 `json:"head"`
	Depth      float64 `json:"depth"`
	BodyType   float64 `json:"bodyType"`
	Proportion float64 `json:"proportion"`
}

// newAvatarBody mengembalikan nil supaya outfit tanpa body terkirim sebagai
// `"body": null`, bukan objek berisi nol yang terbaca seperti data sungguhan.
func newAvatarBody(body *model.AvatarBody) *avatarBodyDTO {
	if body == nil {
		return nil
	}

	out := avatarBodyDTO{}
	if c := body.Colors; c != nil {
		out.Colors = &bodyColorsDTO{
			Head:     c.Head,
			Torso:    c.Torso,
			LeftArm:  c.LeftArm,
			RightArm: c.RightArm,
			LeftLeg:  c.LeftLeg,
			RightLeg: c.RightLeg,
		}
	}
	if s := body.Scales; s != nil {
		out.Scales = &bodyScalesDTO{
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

type outfitItemDTO struct {
	AssetID   int64  `json:"assetId"`
	Slot      string `json:"slot"`
	Name      string `json:"name"`
	AssetType string `json:"assetType"`
	Price     int    `json:"price"`
	// Terisi hanya pada bagian paket. price bagian paket adalah harga bundle
	// induk yang terulang di tiap bagiannya — penjumlah wajib menghitung per
	// bundleId sekali.
	BundleID   int64  `json:"bundleId,omitempty"`
	BundleName string `json:"bundleName,omitempty"`
	// Selalu dikirim, seperti body di level outfit: null berarti klien tidak
	// melaporkan koreksi penempatan, dan pemasang boleh memakai bawaan asset.
	// Tanpa omitempty supaya bentuk item tidak berubah-ubah antar outfit —
	// klien membaca satu bentuk saja, bukan dua. Nol tetap berbeda dari null:
	// nol adalah koreksi eksplisit ke titik asal.
	Adjust *itemAdjustDTO `json:"adjust"`
}

type itemAdjustDTO struct {
	Pos   *vec3DTO `json:"pos"`
	Rot   *vec3DTO `json:"rot"`
	Scale *vec3DTO `json:"scale"`
}

type vec3DTO struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type outfitDetailDTO struct {
	OutfitID         string          `json:"outfitId"`
	ReferenceID      string          `json:"referenceId"`
	RecoItemID       any             `json:"recoItemId"`
	UserID           int64           `json:"userId"`
	TemplateID       string          `json:"templateId"`
	Name             string          `json:"name"`
	IsPublic         bool            `json:"isPublic"`
	CustomTags       []string        `json:"customTags"`
	Items            []outfitItemDTO `json:"items"`
	Body             *avatarBodyDTO  `json:"body"`
	ThumbnailAssetID int64           `json:"thumbnailAssetId,omitempty"`
	LikeCount        int             `json:"likeCount"`
	ViewCount        int             `json:"viewCount"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

// newOutfitItems selalu mengembalikan slice tidak nil supaya outfit kosong
// terkirim sebagai `[]`, bukan `null`.
func newOutfitItems(items []model.OutfitItem) []outfitItemDTO {
	out := make([]outfitItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, outfitItemDTO{
			AssetID:    item.AssetID,
			Slot:       item.Slot,
			Name:       item.Name,
			AssetType:  item.AssetType,
			Price:      item.Price,
			BundleID:   item.BundleID,
			BundleName: item.BundleName,
			Adjust:     newItemAdjust(item.Adjust),
		})
	}
	return out
}

func newItemAdjust(adjust *model.ItemAdjust) *itemAdjustDTO {
	if adjust == nil {
		return nil
	}
	return &itemAdjustDTO{
		Pos:   newVec3(adjust.Pos),
		Rot:   newVec3(adjust.Rot),
		Scale: newVec3(adjust.Scale),
	}
}

func newVec3(v *model.Vec3) *vec3DTO {
	if v == nil {
		return nil
	}
	return &vec3DTO{X: v.X, Y: v.Y, Z: v.Z}
}

func newOutfitDetail(o model.Outfit) outfitDetailDTO {
	return outfitDetailDTO{
		OutfitID:         o.OutfitID,
		ReferenceID:      o.ReferenceID,
		RecoItemID:       nullableString(o.RecoItemID),
		UserID:           o.UserID,
		TemplateID:       o.TemplateID,
		Name:             o.Name,
		IsPublic:         o.IsPublic,
		CustomTags:       o.CustomTags,
		Items:            newOutfitItems(o.Items),
		Body:             newAvatarBody(o.Body),
		ThumbnailAssetID: o.ThumbnailAssetID,
		LikeCount:        o.LikeCount,
		ViewCount:        o.ViewCount,
		CreatedAt:        o.CreatedAt,
		UpdatedAt:        o.UpdatedAt,
	}
}

// --- rig / body template ---------------------------------------------------

type templateDTO struct {
	TemplateID    string    `json:"templateId"`
	Name          string    `json:"name"`
	Gender        string    `json:"gender"`
	SourceAssetID int64     `json:"sourceAssetId"`
	CreatedAt     time.Time `json:"createdAt"`
}

func newTemplate(t model.BodyTemplate) templateDTO {
	return templateDTO{
		TemplateID:    t.TemplateID,
		Name:          t.Name,
		Gender:        t.Gender,
		SourceAssetID: t.SourceAssetID,
		CreatedAt:     t.CreatedAt,
	}
}

func newTemplates(rows []model.BodyTemplate) []templateDTO {
	out := make([]templateDTO, 0, len(rows))
	for _, t := range rows {
		out = append(out, newTemplate(t))
	}
	return out
}

// --- transaksi ------------------------------------------------------------

type txItemResultDTO struct {
	AssetID int64          `json:"assetId"`
	Result  model.TxResult `json:"result"`
}

type txCreatedDTO struct {
	TxID        string            `json:"txId"`
	Status      string            `json:"status"`
	RobuxTotal  int               `json:"robuxTotal"`
	ItemResults []txItemResultDTO `json:"itemResults"`
	ReceivedAt  time.Time         `json:"receivedAt"`
}

func newTxCreated(tx model.Transaction) txCreatedDTO {
	results := make([]txItemResultDTO, 0, len(tx.Items))
	for _, item := range tx.Items {
		results = append(results, txItemResultDTO{AssetID: item.AssetID, Result: item.Result})
	}
	return txCreatedDTO{
		TxID:        tx.TxID,
		Status:      tx.Status,
		RobuxTotal:  tx.RobuxTotal(),
		ItemResults: results,
		ReceivedAt:  tx.ReceivedAt,
	}
}

type txSummaryDTO struct {
	TxID string `json:"txId"`
	// userId selalu ikut, juga pada daftar per pemain: daftar lintas pemain
	// tidak berguna tanpanya, dan bentuk yang berubah-ubah antar dua mode
	// pemanggilan lebih merepotkan klien daripada satu field berlebih.
	UserID     int64     `json:"userId"`
	Status     string    `json:"status"`
	RobuxTotal int       `json:"robuxTotal"`
	ItemCount  int       `json:"itemCount"`
	OccurredAt time.Time `json:"occurredAt"`
}

func newTxSummaries(rows []model.Transaction) []txSummaryDTO {
	out := make([]txSummaryDTO, 0, len(rows))
	for _, tx := range rows {
		out = append(out, txSummaryDTO{
			TxID:       tx.TxID,
			UserID:     tx.UserID,
			Status:     tx.Status,
			RobuxTotal: tx.RobuxTotal(),
			ItemCount:  len(tx.Items),
			OccurredAt: tx.OccurredAt,
		})
	}
	return out
}
