package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Outfits adalah implementasi store.Outfits di atas Postgres.
type Outfits struct {
	pool *pgxpool.Pool
}

// NewOutfits merangkai penyimpanan outfit.
func NewOutfits(pool *pgxpool.Pool) *Outfits { return &Outfits{pool: pool} }

var _ store.Outfits = (*Outfits)(nil)

const outfitColumns = `
	o.outfit_id, o.reference_id::text, o.reco_item_id, o.user_id, o.template_id,
	o.name, o.is_public, o.custom_tags, o.body, o.created_at, o.updated_at, o.deleted_at`

// Create menyimpan outfit beserta itemnya dalam satu transaksi.
func (s *Outfits) Create(ctx context.Context, o model.Outfit) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := ensurePlayer(ctx, tx, o.UserID); err != nil {
			return err
		}

		body, err := marshalBody(o.Body)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO outfit (outfit_id, reference_id, reco_item_id, user_id, template_id,
			                    name, is_public, custom_tags, body, created_at, updated_at, deleted_at)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12)`,
			o.OutfitID, o.ReferenceID, o.RecoItemID, o.UserID, o.TemplateID,
			o.Name, o.IsPublic, tagsOrEmpty(o.CustomTags), body, o.CreatedAt, o.UpdatedAt, o.DeletedAt)
		if err != nil {
			return err
		}
		return insertOutfitItems(ctx, tx, o.OutfitID, o.Items)
	})
}

// Get mengembalikan outfit apa adanya, termasuk yang sudah di-soft-delete.
func (s *Outfits) Get(ctx context.Context, outfitID string) (model.Outfit, error) {
	row := s.pool.QueryRow(ctx, `SELECT`+outfitColumns+` FROM outfit o WHERE o.outfit_id = $1`, outfitID)

	outfit, err := scanOutfit(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Outfit{}, store.ErrNotFound
	}
	if err != nil {
		return model.Outfit{}, err
	}

	items, err := s.itemsFor(ctx, []string{outfitID})
	if err != nil {
		return model.Outfit{}, err
	}
	outfit.Items = items[outfitID]
	return outfit, nil
}

// List mengembalikan satu halaman outfit hidup yang cocok dengan filter.
//
// Paginasi memakai keyset (updated_at menurun, outfit_id menaik) supaya sync
// worker yang mengubah katalog di tengah penelusuran tidak menggeser hasil.
// Filter kosong dilewatkan sebagai NULL sehingga satu query melayani daftar
// per pemain maupun daftar lintas pemain.
func (s *Outfits) List(ctx context.Context, f store.OutfitFilter, after *paging.KeysetCursor, limit int) ([]model.Outfit, bool, error) {
	var (
		cursorAt time.Time
		cursorID string
	)
	if after != nil {
		cursorAt, cursorID = after.At, after.ID
	}

	var userID *int64
	if f.UserID != 0 {
		userID = &f.UserID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT`+outfitColumns+`
		FROM outfit o
		WHERE o.deleted_at IS NULL
		  AND ($1::bigint IS NULL OR o.user_id = $1)
		  AND ($2::boolean IS NULL OR o.is_public = $2)
		  AND ($3::timestamptz IS NULL
		       OR o.updated_at < $3
		       OR (o.updated_at = $3 AND o.outfit_id > $4))
		ORDER BY o.updated_at DESC, o.outfit_id ASC
		LIMIT $5`,
		userID, f.IsPublic, nullableTime(after, cursorAt), cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}

	outfits, err := collectOutfits(rows)
	if err != nil {
		return nil, false, err
	}

	outfits, hasMore := trimPage(outfits, limit)
	if err := s.attachItems(ctx, outfits); err != nil {
		return nil, false, err
	}
	return outfits, hasMore, nil
}

// ListByReferenceIDs mengembalikan outfit hidup untuk sekumpulan referenceId.
func (s *Outfits) ListByReferenceIDs(ctx context.Context, referenceIDs []string) ([]model.Outfit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT`+outfitColumns+`
		FROM outfit o
		WHERE o.reference_id = ANY($1::uuid[])
		  AND o.deleted_at IS NULL
		ORDER BY o.updated_at DESC, o.outfit_id ASC`, referenceIDs)
	if err != nil {
		return nil, err
	}

	outfits, err := collectOutfits(rows)
	if err != nil {
		return nil, err
	}
	if err := s.attachItems(ctx, outfits); err != nil {
		return nil, err
	}
	return outfits, nil
}

// Update mengunci baris, menerapkan fn, lalu menyimpan hasilnya.
//
// Kunci baris diperlukan karena PATCH, PUT items, dan DELETE bisa datang
// bersamaan dari game server yang sama.
func (s *Outfits) Update(ctx context.Context, outfitID string, fn func(*model.Outfit) error) (model.Outfit, error) {
	var updated model.Outfit

	err := s.inTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT`+outfitColumns+` FROM outfit o WHERE o.outfit_id = $1 FOR UPDATE`, outfitID)

		current, err := scanOutfit(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}

		items, err := itemsForTx(ctx, tx, []string{outfitID})
		if err != nil {
			return err
		}
		current.Items = items[outfitID]

		draft := current
		if err := fn(&draft); err != nil {
			return err
		}
		draft.OutfitID = current.OutfitID       // PK tidak boleh berubah
		draft.ReferenceID = current.ReferenceID // referenceId sudah dipegang RecoService

		body, err := marshalBody(draft.Body)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE outfit
			SET reco_item_id = $2, template_id = $3, name = $4, is_public = $5,
			    custom_tags = $6, body = $7::jsonb, updated_at = $8, deleted_at = $9
			WHERE outfit_id = $1`,
			outfitID, draft.RecoItemID, draft.TemplateID, draft.Name, draft.IsPublic,
			tagsOrEmpty(draft.CustomTags), body, draft.UpdatedAt, draft.DeletedAt)
		if err != nil {
			return err
		}

		// Item outfit adalah himpunan, jadi penggantian selalu ditulis utuh.
		// Untuk PATCH yang tidak menyentuh item, isinya sama dan penulisan
		// ulang ini tidak mengubah apa pun.
		if _, err := tx.Exec(ctx, `DELETE FROM outfit_item WHERE outfit_id = $1`, outfitID); err != nil {
			return err
		}
		if err := insertOutfitItems(ctx, tx, outfitID, draft.Items); err != nil {
			return err
		}

		updated = draft
		return nil
	})
	if err != nil {
		return model.Outfit{}, err
	}
	return updated, nil
}

// attachItems mengisi Items untuk sekumpulan outfit dengan satu query.
func (s *Outfits) attachItems(ctx context.Context, outfits []model.Outfit) error {
	if len(outfits) == 0 {
		return nil
	}

	ids := make([]string, 0, len(outfits))
	for _, o := range outfits {
		ids = append(ids, o.OutfitID)
	}

	items, err := s.itemsFor(ctx, ids)
	if err != nil {
		return err
	}
	for i := range outfits {
		outfits[i].Items = items[outfits[i].OutfitID]
	}
	return nil
}

func (s *Outfits) itemsFor(ctx context.Context, outfitIDs []string) (map[string][]model.OutfitItem, error) {
	rows, err := s.pool.Query(ctx, outfitItemsQuery, outfitIDs)
	if err != nil {
		return nil, err
	}
	return collectOutfitItems(rows)
}

func itemsForTx(ctx context.Context, tx pgx.Tx, outfitIDs []string) (map[string][]model.OutfitItem, error) {
	rows, err := tx.Query(ctx, outfitItemsQuery, outfitIDs)
	if err != nil {
		return nil, err
	}
	return collectOutfitItems(rows)
}

const outfitItemsQuery = `
	SELECT outfit_id, asset_id, slot, name, asset_type, price
	FROM outfit_item
	WHERE outfit_id = ANY($1)
	ORDER BY outfit_id, slot`

func collectOutfitItems(rows pgx.Rows) (map[string][]model.OutfitItem, error) {
	defer rows.Close()

	out := make(map[string][]model.OutfitItem)
	for rows.Next() {
		var (
			outfitID string
			item     model.OutfitItem
		)
		if err := rows.Scan(&outfitID, &item.AssetID, &item.Slot,
			&item.Name, &item.AssetType, &item.Price); err != nil {
			return nil, err
		}
		out[outfitID] = append(out[outfitID], item)
	}
	return out, rows.Err()
}

func insertOutfitItems(ctx context.Context, tx pgx.Tx, outfitID string, items []model.OutfitItem) error {
	if len(items) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, item := range items {
		batch.Queue(`
			INSERT INTO outfit_item (outfit_id, asset_id, slot, name, asset_type, price)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			outfitID, item.AssetID, item.Slot, item.Name, item.AssetType, item.Price)
	}

	results := tx.SendBatch(ctx, batch)
	defer results.Close()

	for range items {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// scanner menyatukan pgx.Row dan pgx.Rows agar satu fungsi scan bisa dipakai
// baik untuk QueryRow maupun Query.
type scanner interface {
	Scan(dst ...any) error
}

func scanOutfit(row scanner) (model.Outfit, error) {
	var (
		o    model.Outfit
		body []byte
	)
	err := row.Scan(&o.OutfitID, &o.ReferenceID, &o.RecoItemID, &o.UserID, &o.TemplateID,
		&o.Name, &o.IsPublic, &o.CustomTags, &body, &o.CreatedAt, &o.UpdatedAt, &o.DeletedAt)
	if err != nil {
		return model.Outfit{}, err
	}
	if o.CustomTags == nil {
		o.CustomTags = []string{}
	}
	o.Body, err = unmarshalBody(body)
	if err != nil {
		return model.Outfit{}, err
	}
	o.Items = []model.OutfitItem{}
	return o, nil
}

// OUTFIT.body disimpan sebagai jsonb, bukan dipecah jadi dua belas kolom.
// Isinya blob render yang dilaporkan klien dan dikembalikan apa adanya —
// backend tidak pernah menyaring atau mengurutkan berdasarkan warna maupun
// skala, jadi kolom terpisah hanya menambah lebar tabel tanpa dipakai.
//
// Bentuk JSON-nya ditulis eksplisit di sini supaya isi kolom tidak ikut
// berubah kalau field di paket model diganti nama.
type bodyJSON struct {
	Colors *bodyColorsJSON `json:"colors,omitempty"`
	Scales *bodyScalesJSON `json:"scales,omitempty"`
}

type bodyColorsJSON struct {
	Head     string `json:"head"`
	Torso    string `json:"torso"`
	LeftArm  string `json:"leftArm"`
	RightArm string `json:"rightArm"`
	LeftLeg  string `json:"leftLeg"`
	RightLeg string `json:"rightLeg"`
}

type bodyScalesJSON struct {
	Height     float64 `json:"height"`
	Width      float64 `json:"width"`
	Head       float64 `json:"head"`
	Depth      float64 `json:"depth"`
	BodyType   float64 `json:"bodyType"`
	Proportion float64 `json:"proportion"`
}

func marshalBody(body *model.AvatarBody) ([]byte, error) {
	if body == nil {
		return nil, nil
	}

	out := bodyJSON{}
	if c := body.Colors; c != nil {
		out.Colors = &bodyColorsJSON{
			Head: c.Head, Torso: c.Torso,
			LeftArm: c.LeftArm, RightArm: c.RightArm,
			LeftLeg: c.LeftLeg, RightLeg: c.RightLeg,
		}
	}
	if s := body.Scales; s != nil {
		out.Scales = &bodyScalesJSON{
			Height: s.Height, Width: s.Width, Head: s.Head,
			Depth: s.Depth, BodyType: s.BodyType, Proportion: s.Proportion,
		}
	}
	return json.Marshal(out)
}

func unmarshalBody(raw []byte) (*model.AvatarBody, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var stored bodyJSON
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}

	out := model.AvatarBody{}
	if c := stored.Colors; c != nil {
		out.Colors = &model.BodyColors{
			Head: c.Head, Torso: c.Torso,
			LeftArm: c.LeftArm, RightArm: c.RightArm,
			LeftLeg: c.LeftLeg, RightLeg: c.RightLeg,
		}
	}
	if s := stored.Scales; s != nil {
		out.Scales = &model.BodyScales{
			Height: s.Height, Width: s.Width, Head: s.Head,
			Depth: s.Depth, BodyType: s.BodyType, Proportion: s.Proportion,
		}
	}
	if out.Colors == nil && out.Scales == nil {
		return nil, nil
	}
	return &out, nil
}

func collectOutfits(rows pgx.Rows) ([]model.Outfit, error) {
	defer rows.Close()

	var out []model.Outfit
	for rows.Next() {
		o, err := scanOutfit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
