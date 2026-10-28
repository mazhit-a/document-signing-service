package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
)

type AuditRepository struct {
	pool *pgxpool.Pool
}

func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository {
	return &AuditRepository{pool: pool}
}

func (r *AuditRepository) Append(ctx context.Context, documentID string, typ domain.EventType, actor string, data map[string]any, at time.Time) (domain.AuditEvent, error) {
	if !validUUID(documentID) {
		return domain.AuditEvent{}, domain.ErrNotFound
	}
	if data == nil {
		data = map[string]any{}
	}
	at = at.UTC().Truncate(time.Microsecond)
	db := Conn(ctx, r.pool)

	prev := integrity.GenesisHash
	err := db.QueryRow(ctx, `SELECT hash FROM audit_events WHERE document_id = $1::uuid ORDER BY id DESC LIMIT 1`, documentID).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.AuditEvent{}, fmt.Errorf("read last audit hash: %w", err)
	}

	hash, err := integrity.ChainHash(prev, integrity.Event{Type: string(typ), Actor: actor, Data: data, CreatedAt: at})
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("compute audit hash: %w", err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit data: %w", err)
	}

	ev := domain.AuditEvent{DocumentID: documentID, Type: typ, Actor: actor, Data: data, PrevHash: prev, Hash: hash, CreatedAt: at}
	err = db.QueryRow(ctx,
		`INSERT INTO audit_events (document_id, type, actor, data, prev_hash, hash, created_at)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6, $7) RETURNING id`,
		documentID, string(typ), actor, string(raw), prev, hash, at).Scan(&ev.ID)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("insert audit event: %w", err)
	}
	return ev, nil
}

func (r *AuditRepository) ListByDocument(ctx context.Context, documentID string) ([]domain.AuditEvent, error) {
	if !validUUID(documentID) {
		return nil, nil
	}
	rows, err := Conn(ctx, r.pool).Query(ctx,
		`SELECT id, document_id::text, type, actor, data, prev_hash, hash, created_at
		 FROM audit_events WHERE document_id = $1::uuid ORDER BY id`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var e domain.AuditEvent
		var typ string
		var raw []byte
		if err := rows.Scan(&e.ID, &e.DocumentID, &typ, &e.Actor, &raw, &e.PrevHash, &e.Hash, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		e.Type = domain.EventType(typ)
		if err := json.Unmarshal(raw, &e.Data); err != nil {
			return nil, fmt.Errorf("decode audit data: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
