package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/mazhit-a/mini-kedo/internal/config"
	"github.com/mazhit-a/mini-kedo/internal/notify/email"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if !cfg.IsDev() {
		return fmt.Errorf("outbox-peek prints signing links and only runs when APP_ENV=dev")
	}
	crypto, err := email.NewCrypto(cfg.EmailOutboxKey)
	if err != nil {
		return err
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx,
		`SELECT id::text, kind, recipient, payload_ciphertext FROM email_outbox
		 WHERE status = 'pending' AND kind = 'signer_turn' AND payload_ciphertext IS NOT NULL ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, kind, recipient string
		var sealed []byte
		if err := rows.Scan(&id, &kind, &recipient, &sealed); err != nil {
			return err
		}
		plain, err := crypto.Open(id, email.Kind(kind), sealed)
		if err != nil {
			return fmt.Errorf("row %s: %w", id, err)
		}
		p, err := email.DecodePayload(plain)
		if err != nil {
			return fmt.Errorf("row %s: %w", id, err)
		}
		fmt.Printf("%s  signer %d of %d  %q  %s\n", recipient, p.Position, p.Total, p.DocumentTitle, p.Link)
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count == 0 {
		fmt.Println("no pending signing links")
	}
	return nil
}
