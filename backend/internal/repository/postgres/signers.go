package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

type SignerRepository struct {
	pool *pgxpool.Pool
}

func NewSignerRepository(pool *pgxpool.Pool) *SignerRepository {
	return &SignerRepository{pool: pool}
}

const signerColumns = `id::text, document_id::text, order_index, full_name, email, status::text, COALESCE(token_hash, ''), decided_at, COALESCE(reject_reason, '')`

func scanSigner(row pgx.Row) (domain.Signer, error) {
	var s domain.Signer
	var status string
	err := row.Scan(&s.ID, &s.DocumentID, &s.Order, &s.FullName, &s.Email, &status, &s.TokenHash, &s.DecidedAt, &s.RejectReason)
	s.Status = domain.SignerStatus(status)
	return s, err
}

func querySigners(ctx context.Context, db DBTX, documentID string) ([]domain.Signer, error) {
	rows, err := db.Query(ctx, `SELECT `+signerColumns+` FROM signers WHERE document_id = $1::uuid ORDER BY order_index`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query signers: %w", err)
	}
	defer rows.Close()
	var out []domain.Signer
	for rows.Next() {
		s, err := scanSigner(rows)
		if err != nil {
			return nil, fmt.Errorf("scan signer: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SignerRepository) GetByID(ctx context.Context, id string) (domain.Signer, error) {
	if !validUUID(id) {
		return domain.Signer{}, domain.ErrNotFound
	}
	s, err := scanSigner(Conn(ctx, r.pool).QueryRow(ctx, `SELECT `+signerColumns+` FROM signers WHERE id = $1::uuid`, id))
	if err != nil {
		return domain.Signer{}, mapNoRows(err)
	}
	return s, nil
}

func (r *SignerRepository) GetByTokenHash(ctx context.Context, tokenHash string) (domain.Signer, error) {
	if tokenHash == "" {
		return domain.Signer{}, domain.ErrNotFound
	}
	s, err := scanSigner(Conn(ctx, r.pool).QueryRow(ctx, `SELECT `+signerColumns+` FROM signers WHERE token_hash = $1`, tokenHash))
	if err != nil {
		return domain.Signer{}, mapNoRows(err)
	}
	return s, nil
}

func (r *SignerRepository) ListByDocument(ctx context.Context, documentID string) ([]domain.Signer, error) {
	if !validUUID(documentID) {
		return nil, nil
	}
	return querySigners(ctx, Conn(ctx, r.pool), documentID)
}

func (r *SignerRepository) SetTokenHash(ctx context.Context, id, tokenHash string) error {
	if !validUUID(id) {
		return domain.ErrNotFound
	}
	tag, err := Conn(ctx, r.pool).Exec(ctx, `UPDATE signers SET token_hash = NULLIF($2, '') WHERE id = $1::uuid`, id, tokenHash)
	if err != nil {
		return fmt.Errorf("set token hash: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SignerRepository) UpdateStatus(ctx context.Context, id string, status domain.SignerStatus, decidedAt *time.Time, reason string) error {
	if !validUUID(id) {
		return domain.ErrNotFound
	}
	tag, err := Conn(ctx, r.pool).Exec(ctx,
		`UPDATE signers SET status = $2::signer_status, decided_at = $3, reject_reason = NULLIF($4, '') WHERE id = $1::uuid`,
		id, string(status), decidedAt, reason)
	if err != nil {
		return fmt.Errorf("update signer status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
