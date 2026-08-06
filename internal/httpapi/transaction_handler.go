package httpapi

import (
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/service"
)

type transactionHandler struct {
	transactions *service.Transactions
}

type txItemBody struct {
	AssetID int64          `json:"assetId"`
	Price   int            `json:"price"`
	Result  model.TxResult `json:"result"`
	// bundleId opsional, terisi pada bagian paket: price-nya adalah harga
	// bundle induk yang terulang per bagian, dan perhitungan spend (dasar
	// cashback) menghitung tiap bundleId sekali.
	BundleID int64 `json:"bundleId"`
}

type createTxBody struct {
	UserID     int64        `json:"userId"`
	UniverseID int64        `json:"universeId"`
	PlaceID    int64        `json:"placeId"`
	JobID      string       `json:"jobId"`
	Status     string       `json:"status"`
	OccurredAt time.Time    `json:"occurredAt"`
	Items      []txItemBody `json:"items"`
}

// create menangani POST /v1/transactions.
func (h *transactionHandler) create(w http.ResponseWriter, r *http.Request) {
	var body createTxBody
	if !decodeJSON(w, r, &body) {
		return
	}

	items := make([]model.TransactionItem, 0, len(body.Items))
	for _, item := range body.Items {
		items = append(items, model.TransactionItem{
			AssetID: item.AssetID, Price: item.Price, Result: item.Result, BundleID: item.BundleID,
		})
	}

	tx, replay, err := h.transactions.Create(r.Context(), service.CreateTransactionInput{
		IdempotencyKey: r.Header.Get(idempotencyHeader),
		UserID:         body.UserID,
		UniverseID:     body.UniverseID,
		PlaceID:        body.PlaceID,
		JobID:          body.JobID,
		Status:         body.Status,
		OccurredAt:     body.OccurredAt,
		Items:          items,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	// Replay tidak membuat baris baru, jadi statusnya 200 dan itemResults
	// dihilangkan — klien yang mengulang hanya perlu tahu hasil akhirnya.
	if replay {
		writeJSON(w, http.StatusOK, map[string]any{
			"txId":             tx.TxID,
			"status":           tx.Status,
			"robuxTotal":       tx.RobuxTotal(),
			"idempotentReplay": true,
			"receivedAt":       tx.ReceivedAt,
		})
		return
	}

	w.Header().Set("Location", "/v1/transactions/"+tx.TxID)
	writeJSON(w, http.StatusCreated, newTxCreated(tx))
}

// list menangani GET /v1/transactions.
func (h *transactionHandler) list(w http.ResponseWriter, r *http.Request) {
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

	page, err := h.transactions.List(r.Context(), userID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newListEnvelope(newTxSummaries(page.Transactions), page.NextCursor, page.HasMore))
}
