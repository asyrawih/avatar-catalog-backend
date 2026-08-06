package httpapi

import (
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
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
	UpdatedAt        time.Time       `json:"updatedAt"`
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
		UpdatedAt:        o.UpdatedAt,
	}
}

func newOutfitSummaries(outfits []model.Outfit) []outfitSummaryDTO {
	out := make([]outfitSummaryDTO, 0, len(outfits))
	for _, o := range outfits {
		out = append(out, newOutfitSummary(o))
	}
	return out
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
		})
	}
	return out
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
	TxID       string    `json:"txId"`
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
			Status:     tx.Status,
			RobuxTotal: tx.RobuxTotal(),
			ItemCount:  len(tx.Items),
			OccurredAt: tx.OccurredAt,
		})
	}
	return out
}
