package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mazhit-a/mini-kedo/internal/repository/postgres"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres/pgtest"
	"github.com/mazhit-a/mini-kedo/migrations"
)

const goodHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func mustScan(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return id
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func newUser(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	return mustScan(t, pool, `INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'h', 'N') RETURNING id`, email)
}

func newDoc(t *testing.T, pool *pgxpool.Pool, owner string) string {
	t.Helper()
	return mustScan(t, pool, `INSERT INTO documents (owner_id, title, file_name, content_type, size_bytes, storage_key, sha256)
		VALUES ($1, 't', 'f.pdf', 'application/pdf', 10, 'k', $2) RETURNING id`, owner, goodHash)
}

func newSigner(t *testing.T, pool *pgxpool.Pool, doc string, order int) string {
	t.Helper()
	return mustScan(t, pool, `INSERT INTO signers (document_id, order_index, full_name, email) VALUES ($1, $2, 'S', 's@x.io') RETURNING id`, doc, order)
}

func TestConstraints(t *testing.T) {
	db := pgtest.New(t)
	pool := db.Pool
	ctx := context.Background()
	owner := newUser(t, pool, "o@x.io")
	doc := newDoc(t, pool, owner)
	signer := newSigner(t, pool, doc, 1)

	const (
		uniqueViolation = "23505"
		checkViolation  = "23514"
	)

	insertDoc := `INSERT INTO documents (owner_id, title, file_name, content_type, size_bytes, storage_key, sha256)
		VALUES ($1, $2, 'f', 'c', $3, 'k', $4)`

	tests := []struct {
		name string
		sql  string
		args []any
		code string
	}{
		{"duplicate email", `INSERT INTO users (email, password_hash, full_name) VALUES ('o@x.io', 'h', 'N')`, nil, uniqueViolation},
		{"duplicate document_id+order_index", `INSERT INTO signers (document_id, order_index, full_name, email) VALUES ($1, 1, 'S', 'e')`, []any{doc}, uniqueViolation},
		{"order_index below 1", `INSERT INTO signers (document_id, order_index, full_name, email) VALUES ($1, 0, 'S', 'e')`, []any{doc}, checkViolation},
		{"sha256 too short", insertDoc, []any{owner, "t", 1, "abc"}, checkViolation},
		{"sha256 uppercase", insertDoc, []any{owner, "t", 1, strings.ToUpper(goodHash)}, checkViolation},
		{"sha256 non-hex", insertDoc, []any{owner, "t", 1, strings.Repeat("z", 64)}, checkViolation},
		{"empty title", insertDoc, []any{owner, "", 1, goodHash}, checkViolation},
		{"title 201 chars", insertDoc, []any{owner, strings.Repeat("a", 201), 1, goodHash}, checkViolation},
		{"size zero", insertDoc, []any{owner, "t", 0, goodHash}, checkViolation},
		{"unknown document status", `UPDATE documents SET status = 'bogus' WHERE id = $1`, []any{doc}, "22P02"},
		{"second signature for signer", `INSERT INTO signatures (signer_id, document_id, document_sha256, payload, seal, signed_at) VALUES ($1, $2, 'h', 'p', 's', now())`, []any{signer, doc}, uniqueViolation},
	}

	mustExec(t, pool, `INSERT INTO signatures (signer_id, document_id, document_sha256, payload, seal, signed_at) VALUES ($1, $2, 'h', 'p', 's', now())`, signer, doc)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.sql, tc.args...)
			if got := pgCode(err); got != tc.code {
				t.Fatalf("want SQLSTATE %s, got %q (err=%v)", tc.code, got, err)
			}
		})
	}

	t.Run("valid title boundaries accepted", func(t *testing.T) {
		mustExec(t, pool, insertDoc, owner, "a", 1, goodHash)
		mustExec(t, pool, insertDoc, owner, strings.Repeat("a", 200), 1, goodHash)
	})
}

func TestTokenHashUniqueButNullable(t *testing.T) {
	db := pgtest.New(t)
	pool := db.Pool
	owner := newUser(t, pool, "o@x.io")
	doc := newDoc(t, pool, owner)
	s1 := newSigner(t, pool, doc, 1)
	s2 := newSigner(t, pool, doc, 2)
	s3 := newSigner(t, pool, doc, 3)

	mustExec(t, pool, `UPDATE signers SET token_hash = 'abc' WHERE id = $1`, s1)
	_, err := pool.Exec(context.Background(), `UPDATE signers SET token_hash = 'abc' WHERE id = $1`, s2)
	if pgCode(err) != "23505" {
		t.Fatalf("want unique violation on token_hash, got %v", err)
	}
	mustExec(t, pool, `UPDATE signers SET token_hash = NULL WHERE id IN ($1, $2)`, s2, s3)
}

func TestAuditEventsAppendOnly(t *testing.T) {
	db := pgtest.New(t)
	pool := db.Pool
	ctx := context.Background()
	owner := newUser(t, pool, "o@x.io")
	doc := newDoc(t, pool, owner)
	id := mustScan(t, pool, `INSERT INTO audit_events (document_id, type, actor, prev_hash, hash) VALUES ($1, 'document.created', 'system', 'p', 'h') RETURNING id::text`, doc)

	if _, err := pool.Exec(ctx, `UPDATE audit_events SET actor = 'x' WHERE id = $1::bigint`, id); pgCode(err) != "23001" {
		t.Fatalf("update should be rejected, got %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_events WHERE id = $1::bigint`, id); pgCode(err) != "23001" {
		t.Fatalf("delete should be rejected, got %v", err)
	}
	mustExec(t, pool, `INSERT INTO audit_events (document_id, type, actor, prev_hash, hash) VALUES ($1, 'document.sent', 'system', 'h', 'h2')`, doc)
}

func TestDocumentFileImmutable(t *testing.T) {
	db := pgtest.New(t)
	pool := db.Pool
	ctx := context.Background()
	owner := newUser(t, pool, "o@x.io")
	doc := newDoc(t, pool, owner)

	for _, col := range []string{"sha256 = '" + strings.Repeat("a", 64) + "'", "storage_key = 'other'", "file_name = 'other'", "size_bytes = 99", "content_type = 'x'"} {
		_, err := pool.Exec(ctx, `UPDATE documents SET `+col+` WHERE id = $1`, doc)
		if pgCode(err) != "23001" {
			t.Fatalf("updating %s should be rejected, got %v", col, err)
		}
	}
	mustExec(t, pool, `UPDATE documents SET status = 'in_progress', updated_at = now() WHERE id = $1`, doc)
}

func TestMigrationsRoundTrip(t *testing.T) {
	db := pgtest.New(t)
	if err := postgres.MigrateDown(db.URL, migrations.FS); err != nil {
		t.Fatalf("down: %v", err)
	}
	var tables int
	err := db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name <> 'schema_migrations'`).Scan(&tables)
	if err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("expected no tables after down, got %d", tables)
	}
	if err := postgres.MigrateUp(db.URL, migrations.FS); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if err := postgres.MigrateUp(db.URL, migrations.FS); err != nil {
		t.Fatalf("repeat up should be a no-op: %v", err)
	}
}

func TestDueDeliveriesPartialIndex(t *testing.T) {
	db := pgtest.New(t)
	var def string
	err := db.Pool.QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_deliveries_due'`).Scan(&def)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "WHERE") || !strings.Contains(def, "pending") {
		t.Fatalf("index is not partial on pending: %s", def)
	}
}
