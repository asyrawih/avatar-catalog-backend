package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hanan/avatar-catalog-backend/internal/model"
	"github.com/hanan/avatar-catalog-backend/internal/paging"
	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Templates adalah implementasi store.Templates di atas Postgres.
type Templates struct {
	pool *pgxpool.Pool
}

// NewTemplates merangkai registry rig.
func NewTemplates(pool *pgxpool.Pool) *Templates { return &Templates{pool: pool} }

var _ store.Templates = (*Templates)(nil)

const templateColumns = ` template_id, name, gender, source_asset_id, created_at`

// Get mengembalikan rig berdasarkan templateId (Roblox asset id).
func (s *Templates) Get(ctx context.Context, templateID string) (model.BodyTemplate, error) {
	row := s.pool.QueryRow(ctx, `SELECT`+templateColumns+` FROM body_template WHERE template_id = $1`, templateID)

	tpl, err := scanTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.BodyTemplate{}, store.ErrNotFound
	}
	if err != nil {
		return model.BodyTemplate{}, err
	}
	return tpl, nil
}

// Ensure mendaftarkan rig bila belum ada.
//
// ON CONFLICT DO NOTHING, bukan DO UPDATE: nama dan gender yang sudah diisi
// lewat PATCH tidak boleh terhapus hanya karena outfit baru memakai rig yang
// sama. RETURNING tidak terisi saat konflik, jadi barisnya dibaca ulang.
func (s *Templates) Ensure(ctx context.Context, tpl model.BodyTemplate) (model.BodyTemplate, bool, error) {
	if tpl.CreatedAt.IsZero() {
		tpl.CreatedAt = time.Now().UTC()
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO body_template (template_id, name, gender, source_asset_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (template_id) DO NOTHING
		RETURNING`+templateColumns,
		tpl.TemplateID, tpl.Name, tpl.Gender, nullableInt64(tpl.SourceAssetID), tpl.CreatedAt)

	created, err := scanTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// Konflik: rig sudah terdaftar, baris lamanya yang berlaku.
		existing, err := s.Get(ctx, tpl.TemplateID)
		return existing, false, err
	}
	if err != nil {
		return model.BodyTemplate{}, false, err
	}
	return created, true, nil
}

// List mengembalikan satu halaman rig terdaftar, terbaru dulu.
func (s *Templates) List(ctx context.Context, after *paging.KeysetCursor, limit int) ([]model.BodyTemplate, bool, error) {
	var (
		cursorAt time.Time
		cursorID string
	)
	if after != nil {
		cursorAt, cursorID = after.At, after.ID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT`+templateColumns+`
		FROM body_template
		WHERE ($1::timestamptz IS NULL
		       OR created_at < $1
		       OR (created_at = $1 AND template_id > $2))
		ORDER BY created_at DESC, template_id ASC
		LIMIT $3`, nullableTime(after, cursorAt), cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	templates := make([]model.BodyTemplate, 0, limit)
	for rows.Next() {
		tpl, err := scanTemplate(rows)
		if err != nil {
			return nil, false, err
		}
		templates = append(templates, tpl)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	templates, hasMore := trimPage(templates, limit)
	return templates, hasMore, nil
}

// Update mengunci baris, menerapkan fn, lalu menyimpan hasilnya.
func (s *Templates) Update(ctx context.Context, templateID string, fn func(*model.BodyTemplate) error) (model.BodyTemplate, error) {
	var updated model.BodyTemplate

	err := runInTx(ctx, s.pool.Begin, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT`+templateColumns+` FROM body_template WHERE template_id = $1 FOR UPDATE`, templateID)

		current, err := scanTemplate(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}

		draft := current
		if err := fn(&draft); err != nil {
			return err
		}
		draft.TemplateID = current.TemplateID // PK tidak boleh berubah

		_, err = tx.Exec(ctx, `
			UPDATE body_template SET name = $2, gender = $3, source_asset_id = $4
			WHERE template_id = $1`,
			templateID, draft.Name, draft.Gender, nullableInt64(draft.SourceAssetID))
		if err != nil {
			return err
		}

		updated = draft
		return nil
	})
	if err != nil {
		return model.BodyTemplate{}, err
	}
	return updated, nil
}

func scanTemplate(row scanner) (model.BodyTemplate, error) {
	var (
		tpl           model.BodyTemplate
		sourceAssetID *int64
	)
	err := row.Scan(&tpl.TemplateID, &tpl.Name, &tpl.Gender, &sourceAssetID, &tpl.CreatedAt)
	if err != nil {
		return model.BodyTemplate{}, err
	}
	if sourceAssetID != nil {
		tpl.SourceAssetID = *sourceAssetID
	}
	return tpl, nil
}
