package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

type DocumentRepository struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

func NewDocumentRepository(pool *pgxpool.Pool, tx *TxManager) *DocumentRepository {
	return &DocumentRepository{pool: pool, tx: tx}
}

const documentColumns = `id::text, owner_id::text, title, file_name, content_type, size_bytes, storage_key, sha256, status::text, created_at, updated_at, completed_at`

func scanDocument(row pgx.Row) (domain.Document, error) {
	var d domain.Document
	var status string
	err := row.Scan(&d.ID, &d.OwnerID, &d.Title, &d.FileName, &d.ContentType, &d.SizeBytes, &d.StorageKey, &d.SHA256, &status, &d.CreatedAt, &d.UpdatedAt, &d.CompletedAt)
	d.Status = domain.DocumentStatus(status)
	return d, err
}

func (r *DocumentRepository) Create(ctx context.Context, d domain.Document) error {
	return r.tx.WithinTx(ctx, func(ctx context.Context) error {
		db := Conn(ctx, r.pool)
		_, err := db.Exec(ctx,
			`INSERT INTO documents (id, owner_id, title, file_name, content_type, size_bytes, storage_key, sha256, status, created_at, updated_at, completed_at)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9::document_status, $10, $11, $12)`,
			d.ID, d.OwnerID, d.Title, d.FileName, d.ContentType, d.SizeBytes, d.StorageKey, d.SHA256, string(d.Status), d.CreatedAt, d.UpdatedAt, d.CompletedAt)
		if err != nil {
			return fmt.Errorf("insert document: %w", err)
		}
		for _, s := range d.Signers {
			_, err := db.Exec(ctx,
				`INSERT INTO signers (id, document_id, order_index, full_name, email, status, token_hash, decided_at, reject_reason)
				 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::signer_status, NULLIF($7, ''), $8, NULLIF($9, ''))`,
				s.ID, d.ID, s.Order, s.FullName, s.Email, string(s.Status), s.TokenHash, s.DecidedAt, s.RejectReason)
			if err != nil {
				return fmt.Errorf("insert signer: %w", err)
			}
		}
		return nil
	})
}

func (r *DocumentRepository) load(ctx context.Context, query string, args ...any) (domain.Document, error) {
	db := Conn(ctx, r.pool)
	d, err := scanDocument(db.QueryRow(ctx, query, args...))
	if err != nil {
		return domain.Document{}, mapNoRows(err)
	}
	d.Signers, err = querySigners(ctx, db, d.ID)
	if err != nil {
		return domain.Document{}, err
	}
	return d, nil
}

func (r *DocumentRepository) GetForOwner(ctx context.Context, ownerID, id string) (domain.Document, error) {
	if !validUUID(ownerID) || !validUUID(id) {
		return domain.Document{}, domain.ErrNotFound
	}
	return r.load(ctx, `SELECT `+documentColumns+` FROM documents WHERE id = $1::uuid AND owner_id = $2::uuid`, id, ownerID)
}

func (r *DocumentRepository) GetByID(ctx context.Context, id string) (domain.Document, error) {
	if !validUUID(id) {
		return domain.Document{}, domain.ErrNotFound
	}
	return r.load(ctx, `SELECT `+documentColumns+` FROM documents WHERE id = $1::uuid`, id)
}

func (r *DocumentRepository) GetForUpdate(ctx context.Context, id string) (domain.Document, error) {
	if !validUUID(id) {
		return domain.Document{}, domain.ErrNotFound
	}
	return r.load(ctx, `SELECT `+documentColumns+` FROM documents WHERE id = $1::uuid FOR UPDATE`, id)
}

func (r *DocumentRepository) List(ctx context.Context, p service.DocumentListParams) (service.DocumentPage, error) {
	if !validUUID(p.OwnerID) {
		return service.DocumentPage{}, nil
	}
	if p.Limit < 1 {
		return service.DocumentPage{}, domain.NewValidationError(map[string]string{"limit": "must be at least 1"})
	}
	var status any
	if p.Status != nil {
		status = string(*p.Status)
	}
	var afterAt any
	var afterID any
	if p.After != nil {
		if !validUUID(p.After.ID) {
			return service.DocumentPage{}, domain.NewValidationError(map[string]string{"cursor": "invalid"})
		}
		afterAt = p.After.CreatedAt
		afterID = p.After.ID
	}

	rows, err := Conn(ctx, r.pool).Query(ctx,
		`SELECT `+documentColumns+` FROM documents
		 WHERE owner_id = $1::uuid
		   AND ($2::text IS NULL OR status = $2::document_status)
		   AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::uuid))
		 ORDER BY created_at DESC, id DESC
		 LIMIT $5`,
		p.OwnerID, status, afterAt, afterID, p.Limit+1)
	if err != nil {
		return service.DocumentPage{}, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	var items []domain.Document
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return service.DocumentPage{}, fmt.Errorf("scan document: %w", err)
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return service.DocumentPage{}, fmt.Errorf("list documents: %w", err)
	}
	rows.Close()

	page := service.DocumentPage{}
	if len(items) > p.Limit {
		items = items[:p.Limit]
		last := items[len(items)-1]
		page.Next = &service.DocumentCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	for i := range items {
		items[i].Signers, err = querySigners(ctx, Conn(ctx, r.pool), items[i].ID)
		if err != nil {
			return service.DocumentPage{}, err
		}
	}
	page.Items = items
	return page, nil
}

func (r *DocumentRepository) UpdateStatus(ctx context.Context, id string, status domain.DocumentStatus, updatedAt time.Time, completedAt *time.Time) error {
	if !validUUID(id) {
		return domain.ErrNotFound
	}
	tag, err := Conn(ctx, r.pool).Exec(ctx,
		`UPDATE documents SET status = $2::document_status, updated_at = $3, completed_at = $4 WHERE id = $1::uuid`,
		id, string(status), updatedAt, completedAt)
	if err != nil {
		return fmt.Errorf("update document status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
