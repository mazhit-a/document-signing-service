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
	"github.com/mazhit-a/mini-kedo/internal/notify/webhook"
)

type WebhookRepository struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

func NewWebhookRepository(pool *pgxpool.Pool, tx *TxManager) *WebhookRepository {
	return &WebhookRepository{pool: pool, tx: tx}
}

func (r *WebhookRepository) Create(ctx context.Context, e domain.WebhookEndpoint) error {
	if !validUUID(e.OwnerID) || !validUUID(e.ID) {
		return domain.ErrNotFound
	}
	_, err := Conn(ctx, r.pool).Exec(ctx,
		`INSERT INTO webhook_endpoints (id, owner_id, url, secret, active, created_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)`,
		e.ID, e.OwnerID, e.URL, e.Secret, e.Active, e.CreatedAt.UTC().Truncate(time.Microsecond))
	if err != nil {
		return fmt.Errorf("insert webhook endpoint: %w", err)
	}
	return nil
}

func (r *WebhookRepository) ListByOwner(ctx context.Context, ownerID string) ([]domain.WebhookEndpoint, error) {
	if !validUUID(ownerID) {
		return nil, nil
	}
	rows, err := Conn(ctx, r.pool).Query(ctx,
		`SELECT id::text, owner_id::text, url, secret, active, created_at
		 FROM webhook_endpoints WHERE owner_id = $1::uuid ORDER BY created_at, id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("query webhook endpoints: %w", err)
	}
	defer rows.Close()
	var out []domain.WebhookEndpoint
	for rows.Next() {
		var e domain.WebhookEndpoint
		if err := rows.Scan(&e.ID, &e.OwnerID, &e.URL, &e.Secret, &e.Active, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan webhook endpoint: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *WebhookRepository) Delete(ctx context.Context, ownerID, id string) error {
	if !validUUID(ownerID) || !validUUID(id) {
		return domain.ErrNotFound
	}
	tag, err := Conn(ctx, r.pool).Exec(ctx,
		`DELETE FROM webhook_endpoints WHERE id = $1::uuid AND owner_id = $2::uuid`, id, ownerID)
	if err != nil {
		return fmt.Errorf("delete webhook endpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *WebhookRepository) Enqueue(ctx context.Context, event domain.AuditEvent) error {
	if !validUUID(event.DocumentID) {
		return domain.ErrNotFound
	}
	_, err := Conn(ctx, r.pool).Exec(ctx,
		`INSERT INTO webhook_deliveries (endpoint_id, event_id, idempotency_key)
		 SELECT e.id, $1::bigint, $1::bigint::text || ':' || e.id::text
		 FROM webhook_endpoints e
		 JOIN documents d ON d.owner_id = e.owner_id
		 WHERE d.id = $2::uuid AND e.active
		 ON CONFLICT (idempotency_key) DO NOTHING`,
		event.ID, event.DocumentID)
	if err != nil {
		return fmt.Errorf("enqueue webhook deliveries: %w", err)
	}
	return nil
}

func (r *WebhookRepository) ProcessNext(ctx context.Context, now time.Time, handle func(ctx context.Context, d webhook.Delivery) (webhook.Outcome, error)) (bool, error) {
	found := false
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		db := Conn(ctx, r.pool)
		var d webhook.Delivery
		var raw []byte
		err := db.QueryRow(ctx,
			`SELECT d.id::text, d.idempotency_key, d.attempts, e.url, e.secret,
			        a.type, a.document_id::text, a.data, a.created_at
			 FROM webhook_deliveries d
			 JOIN webhook_endpoints e ON e.id = d.endpoint_id
			 JOIN audit_events a ON a.id = d.event_id
			 WHERE d.status = 'pending' AND d.next_attempt_at <= $1
			 ORDER BY d.next_attempt_at, d.id
			 LIMIT 1
			 FOR UPDATE OF d SKIP LOCKED`, now).
			Scan(&d.ID, &d.IdempotencyKey, &d.Attempts, &d.URL, &d.Secret, &d.EventType, &d.DocumentID, &raw, &d.EventAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim webhook delivery: %w", err)
		}
		if err := json.Unmarshal(raw, &d.EventData); err != nil {
			return fmt.Errorf("decode event data: %w", err)
		}

		out, err := handle(ctx, d)
		if err != nil {
			return err
		}

		var next any = now
		if out.Status == webhook.StatusPending {
			next = out.NextAttemptAt.UTC().Truncate(time.Microsecond)
		}
		var lastErr any
		if out.Error != "" {
			lastErr = out.Error
		}
		if _, err := db.Exec(ctx,
			`UPDATE webhook_deliveries
			 SET status = $2, attempts = attempts + 1, next_attempt_at = $3, last_error = $4
			 WHERE id = $1::uuid`,
			d.ID, string(out.Status), next, lastErr); err != nil {
			return fmt.Errorf("update webhook delivery: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}
