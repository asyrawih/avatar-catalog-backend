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

// --- outfit ---------------------------------------------------------------

type outfitSummaryDTO struct {
	OutfitID    string    `json:"outfitId"`
	ReferenceID string    `json:"referenceId"`
	Name        string    `json:"name"`
	TemplateID  string    `json:"templateId"`
	IsPublic    bool      `json:"isPublic"`
	ItemCount   int       `json:"itemCount"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func newOutfitSummary(o model.Outfit) outfitSummaryDTO {
	return outfitSummaryDTO{
		OutfitID:    o.OutfitID,
		ReferenceID: o.ReferenceID,
		Name:        o.Name,
		TemplateID:  o.TemplateID,
		IsPublic:    o.IsPublic,
		ItemCount:   len(o.Items),
		UpdatedAt:   o.UpdatedAt,
	}
}

func newOutfitSummaries(outfits []model.Outfit) []outfitSummaryDTO {
	out := make([]outfitSummaryDTO, 0, len(outfits))
	for _, o := range outfits {
		out = append(out, newOutfitSummary(o))
	}
	return out
}

type outfitItemDTO struct {
	AssetID   int64  `json:"assetId"`
	Slot      string `json:"slot"`
	Name      string `json:"name"`
	AssetType string `json:"assetType"`
	Price     int    `json:"price"`
}

type outfitDetailDTO struct {
	OutfitID    string          `json:"outfitId"`
	ReferenceID string          `json:"referenceId"`
	RecoItemID  any             `json:"recoItemId"`
	UserID      int64           `json:"userId"`
	TemplateID  string          `json:"templateId"`
	Name        string          `json:"name"`
	IsPublic    bool            `json:"isPublic"`
	CustomTags  []string        `json:"customTags"`
	Items       []outfitItemDTO `json:"items"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

func newOutfitDetail(o model.Outfit) outfitDetailDTO {
	items := make([]outfitItemDTO, 0, len(o.Items))
	for _, item := range o.Items {
		items = append(items, outfitItemDTO{
			AssetID:   item.AssetID,
			Slot:      item.Slot,
			Name:      item.Name,
			AssetType: item.AssetType,
			Price:     item.Price,
		})
	}

	return outfitDetailDTO{
		OutfitID:    o.OutfitID,
		ReferenceID: o.ReferenceID,
		RecoItemID:  nullableString(o.RecoItemID),
		UserID:      o.UserID,
		TemplateID:  o.TemplateID,
		Name:        o.Name,
		IsPublic:    o.IsPublic,
		CustomTags:  o.CustomTags,
		Items:       items,
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
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
