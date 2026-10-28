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
	"github.com/mazhit-a/mini-kedo/internal/notify/email"
)

type EmailRepository struct {
	pool   *pgxpool.Pool
	tx     *TxManager
	crypto *email.Crypto
	newID  func() string
}

func NewEmailRepository(pool *pgxpool.Pool, tx *TxManager, crypto *email.Crypto, newID func() string) *EmailRepository {
	return &EmailRepository{pool: pool, tx: tx, crypto: crypto, newID: newID}
}

func (r *EmailRepository) Enqueue(ctx context.Context, out email.Outgoing) error {
	if !validUUID(out.DocumentID) || !out.Kind.Valid() {
		return domain.ErrNotFound
	}
	plain, err := json.Marshal(out.Payload)
	if err != nil {
		return fmt.Errorf("encode email payload: %w", err)
	}
	id := r.newID()
	sealed, err := r.crypto.Seal(id, out.Kind, plain)
	if err != nil {
		return err
	}
	var at any
	if !out.At.IsZero() {
		at = out.At.UTC().Truncate(time.Microsecond)
	}
	_, err = Conn(ctx, r.pool).Exec(ctx,
		`INSERT INTO email_outbox (id, document_id, kind, recipient, payload_ciphertext, next_attempt_at, created_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, COALESCE($6::timestamptz, now()), COALESCE($6::timestamptz, now()))`,
		id, out.DocumentID, string(out.Kind), out.Recipient, sealed, at)
	if err != nil {
		return fmt.Errorf("insert email outbox: %w", err)
	}
	return nil
}

func (r *EmailRepository) PeekPending(ctx context.Context, documentID string) ([]email.Outgoing, error) {
	if !validUUID(documentID) {
		return nil, nil
	}
	rows, err := Conn(ctx, r.pool).Query(ctx,
		`SELECT id::text, kind, recipient, payload_ciphertext
		 FROM email_outbox
		 WHERE document_id = $1::uuid AND status = 'pending' AND payload_ciphertext IS NOT NULL
		 ORDER BY created_at, id`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query email outbox: %w", err)
	}
	defer rows.Close()
	var out []email.Outgoing
	for rows.Next() {
		var id, kind, recipient string
		var sealed []byte
		if err := rows.Scan(&id, &kind, &recipient, &sealed); err != nil {
			return nil, fmt.Errorf("scan email outbox: %w", err)
		}
		plain, err := r.crypto.Open(id, email.Kind(kind), sealed)
		if err != nil {
			return nil, err
		}
		var p email.Payload
		if err := json.Unmarshal(plain, &p); err != nil {
			return nil, fmt.Errorf("decode email payload: %w", err)
		}
		out = append(out, email.Outgoing{DocumentID: documentID, Kind: email.Kind(kind), Recipient: recipient, Payload: p})
	}
	return out, rows.Err()
}

func (r *EmailRepository) ProcessNext(ctx context.Context, now time.Time, handle func(ctx context.Context, it email.Item) (email.Outcome, error)) (bool, error) {
	found := false
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		db := Conn(ctx, r.pool)
		var it email.Item
		var kind string
		err := db.QueryRow(ctx,
			`SELECT id::text, kind, recipient, attempts, payload_ciphertext
			 FROM email_outbox
			 WHERE status = 'pending' AND next_attempt_at <= $1
			 ORDER BY next_attempt_at, id
			 LIMIT 1
			 FOR UPDATE SKIP LOCKED`, now).
			Scan(&it.ID, &kind, &it.Recipient, &it.Attempts, &it.Ciphertext)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim email outbox row: %w", err)
		}
		it.Kind = email.Kind(kind)

		out, err := handle(ctx, it)
		if err != nil {
			return err
		}

		next := now
		if out.Status == email.StatusPending {
			next = out.NextAttemptAt.UTC().Truncate(time.Microsecond)
		}
		var lastErr any
		if out.Error != "" {
			lastErr = out.Error
		}
		var sentAt any
		if out.Status == email.StatusSent {
			sentAt = now.UTC().Truncate(time.Microsecond)
		}
		if _, err := db.Exec(ctx,
			`UPDATE email_outbox
			 SET status = $2, attempts = attempts + 1, next_attempt_at = $3, last_error = $4, sent_at = $5,
			     payload_ciphertext = CASE WHEN $2 = 'pending' THEN payload_ciphertext ELSE NULL END
			 WHERE id = $1::uuid`,
			it.ID, string(out.Status), next, lastErr, sentAt); err != nil {
			return fmt.Errorf("update email outbox: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}
