package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

type SignatureRepository struct {
	pool *pgxpool.Pool
}

func NewSignatureRepository(pool *pgxpool.Pool) *SignatureRepository {
	return &SignatureRepository{pool: pool}
}

const signatureColumns = `id::text, signer_id::text, document_id::text, document_sha256, payload, seal, signed_at, COALESCE(host(ip), ''), COALESCE(user_agent, '')`

func scanSignature(row pgx.Row) (domain.Signature, error) {
	var s domain.Signature
	err := row.Scan(&s.ID, &s.SignerID, &s.DocumentID, &s.DocumentSHA256, &s.Payload, &s.Seal, &s.SignedAt, &s.IP, &s.UserAgent)
	return s, err
}

func (r *SignatureRepository) Insert(ctx context.Context, s domain.Signature) error {
	_, err := Conn(ctx, r.pool).Exec(ctx,
		`INSERT INTO signatures (id, signer_id, document_id, document_sha256, payload, seal, signed_at, ip, user_agent)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, NULLIF($8, '')::inet, NULLIF($9, ''))`,
		s.ID, s.SignerID, s.DocumentID, s.DocumentSHA256, s.Payload, s.Seal, s.SignedAt, s.IP, s.UserAgent)
	if err != nil {
		return fmt.Errorf("insert signature: %w", err)
	}
	return nil
}

func (r *SignatureRepository) ListByDocument(ctx context.Context, documentID string) ([]domain.Signature, error) {
	if !validUUID(documentID) {
		return nil, nil
	}
	rows, err := Conn(ctx, r.pool).Query(ctx,
		`SELECT `+signatureColumns+` FROM signatures s WHERE document_id = $1::uuid
		 ORDER BY (SELECT order_index FROM signers WHERE signers.id = s.signer_id)`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query signatures: %w", err)
	}
	defer rows.Close()
	var out []domain.Signature
	for rows.Next() {
		s, err := scanSignature(rows)
		if err != nil {
			return nil, fmt.Errorf("scan signature: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SignatureRepository) GetBySigner(ctx context.Context, signerID string) (domain.Signature, error) {
	if !validUUID(signerID) {
		return domain.Signature{}, domain.ErrNotFound
	}
	s, err := scanSignature(Conn(ctx, r.pool).QueryRow(ctx, `SELECT `+signatureColumns+` FROM signatures WHERE signer_id = $1::uuid`, signerID))
	if err != nil {
		return domain.Signature{}, mapNoRows(err)
	}
	return s, nil
}
