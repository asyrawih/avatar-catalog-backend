package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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
	o.name, o.is_public, o.custom_tags, o.body, coalesce(o.thumbnail_asset_id, 0),
	o.created_at, o.updated_at, o.deleted_at`

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
			                    name, is_public, custom_tags, body, thumbnail_asset_id,
			                    created_at, updated_at, deleted_at)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12, $13)`,
			o.OutfitID, o.ReferenceID, o.RecoItemID, o.UserID, o.TemplateID,
			o.Name, o.IsPublic, tagsOrEmpty(o.CustomTags), body, nullableInt64(o.ThumbnailAssetID),
			o.CreatedAt, o.UpdatedAt, o.DeletedAt)
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

	var outfitIDs []string
	if len(f.OutfitIDs) > 0 {
		outfitIDs = f.OutfitIDs
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
		  AND ($6::text[] IS NULL OR o.outfit_id = ANY($6))
		  AND ($7::text IS NULL OR o.name ILIKE $7 ESCAPE '\')
		ORDER BY o.updated_at DESC, o.outfit_id ASC
		LIMIT $5`,
		userID, f.IsPublic, nullableTime(after, cursorAt), cursorID, limit+1,
		outfitIDs, likePattern(f.Keyword))
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

// Search mengembalikan outfit hidup terurut dari yang paling mirip dengan
// f.Keyword, menggabungkan dua peringkat dengan Reciprocal Rank Fusion:
//
//   - leksikal : word_similarity pg_trgm — "zepeto" tetap menemukan
//     "Aiche ZAPPETO" walau salah ketik. Operator <% memakai
//     outfit_name_trgm_idx; ambangnya diset 0.3 di level database
//     (lihat db/init/001_schema.sql).
//   - semantik : jarak cosine pgvector di name_embedding — "jaket" bisa
//     menemukan "Jacket". Hanya ikut bila qEmbedding terisi DAN barisnya
//     sudah di-embed; selain itu cabang ini kosong dan pencarian tetap
//     jalan leksikal saja.
//
// Konstanta 60 pada RRF adalah nilai baku dari papernya; kedua cabang
// dibatasi 50 kandidat supaya fusi bekerja pada kandidat terbaik saja.
func (s *Outfits) Search(ctx context.Context, f store.OutfitFilter, qEmbedding []float32, limit int) ([]model.Outfit, error) {
	var userID *int64
	if f.UserID != 0 {
		userID = &f.UserID
	}

	rows, err := s.pool.Query(ctx, `
		WITH lexical AS (
			SELECT o.outfit_id,
			       row_number() OVER (
			           ORDER BY word_similarity($1, o.name) DESC, o.updated_at DESC, o.outfit_id
			       ) AS rank
			FROM outfit o
			WHERE o.deleted_at IS NULL
			  AND ($2::bigint IS NULL OR o.user_id = $2)
			  AND ($3::boolean IS NULL OR o.is_public = $3)
			  AND $1 <% o.name
			ORDER BY rank
			LIMIT 50
		),
		semantic AS (
			SELECT o.outfit_id,
			       row_number() OVER (
			           ORDER BY o.name_embedding <=> $4::vector, o.outfit_id
			       ) AS rank
			FROM outfit o
			WHERE o.deleted_at IS NULL
			  AND ($2::bigint IS NULL OR o.user_id = $2)
			  AND ($3::boolean IS NULL OR o.is_public = $3)
			  AND o.name_embedding IS NOT NULL
			  AND $4::text IS NOT NULL
			ORDER BY rank
			LIMIT 50
		)
		SELECT`+outfitColumns+`
		FROM lexical l
		FULL JOIN semantic s USING (outfit_id)
		JOIN outfit o USING (outfit_id)
		ORDER BY coalesce(1.0/(60 + l.rank), 0) + coalesce(1.0/(60 + s.rank), 0) DESC,
		         o.updated_at DESC, o.outfit_id
		LIMIT $5`,
		f.Keyword, userID, f.IsPublic, vectorLiteral(qEmbedding), limit)
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

// SetNameEmbedding menyimpan embedding nama sebuah outfit.
func (s *Outfits) SetNameEmbedding(ctx context.Context, outfitID string, embedding []float32) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outfit SET name_embedding = $2::vector WHERE outfit_id = $1`,
		outfitID, vectorLiteral(embedding))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// vectorLiteral menuliskan embedding dalam bentuk teks pgvector ("[1,2,3]").
// Lewat teks supaya tidak perlu mendaftarkan tipe vector di pgx; nil menjadi
// NULL sehingga cabang semantik query pencarian kosong dengan sendirinya.
func vectorLiteral(embedding []float32) *string {
	if len(embedding) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteByte('[')
	for i, v := range embedding {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	b.WriteByte(']')
	out := b.String()
	return &out
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
			    custom_tags = $6, body = $7::jsonb, thumbnail_asset_id = $8,
			    updated_at = $9, deleted_at = $10
			WHERE outfit_id = $1`,
			outfitID, draft.RecoItemID, draft.TemplateID, draft.Name, draft.IsPublic,
			tagsOrEmpty(draft.CustomTags), body, nullableInt64(draft.ThumbnailAssetID),
			draft.UpdatedAt, draft.DeletedAt)
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
	SELECT outfit_id, asset_id, slot, name, asset_type, price,
	       coalesce(bundle_id, 0), bundle_name
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
			&item.Name, &item.AssetType, &item.Price,
			&item.BundleID, &item.BundleName); err != nil {
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
			INSERT INTO outfit_item (outfit_id, asset_id, slot, name, asset_type, price,
			                         bundle_id, bundle_name)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			outfitID, item.AssetID, item.Slot, item.Name, item.AssetType, item.Price,
			nullableInt64(item.BundleID), item.BundleName)
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
		&o.Name, &o.IsPublic, &o.CustomTags, &body, &o.ThumbnailAssetID,
		&o.CreatedAt, &o.UpdatedAt, &o.DeletedAt)
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
