package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/repository/postgres"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres/pgtest"
)

func countUsers(t *testing.T, db *pgtest.DB) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func insertUser(ctx context.Context, db *pgtest.DB, email string) error {
	_, err := postgres.Conn(ctx, db.Pool).Exec(ctx, `INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'h', 'N')`, email)
	return err
}

func TestTxManagerCommit(t *testing.T) {
	db := pgtest.New(t)
	tm := postgres.NewTxManager(db.Pool)
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		return insertUser(ctx, db, "a@x.io")
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := countUsers(t, db); n != 1 {
		t.Fatalf("want 1 user after commit, got %d", n)
	}
}

func TestTxManagerRollbackOnError(t *testing.T) {
	db := pgtest.New(t)
	tm := postgres.NewTxManager(db.Pool)
	boom := errors.New("boom")
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := insertUser(ctx, db, "a@x.io"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if n := countUsers(t, db); n != 0 {
		t.Fatalf("want 0 users after rollback, got %d", n)
	}
}

func TestTxManagerVisibilityInsideTxOnly(t *testing.T) {
	db := pgtest.New(t)
	tm := postgres.NewTxManager(db.Pool)
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := insertUser(ctx, db, "a@x.io"); err != nil {
			return err
		}
		var inside int
		if err := postgres.Conn(ctx, db.Pool).QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&inside); err != nil {
			return err
		}
		if inside != 1 {
			t.Errorf("tx should see own write, got %d", inside)
		}
		if outside := countUsers(t, db); outside != 0 {
			t.Errorf("uncommitted write leaked outside tx: %d", outside)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTxManagerNestedReusesTx(t *testing.T) {
	db := pgtest.New(t)
	tm := postgres.NewTxManager(db.Pool)
	boom := errors.New("boom")

	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := insertUser(ctx, db, "outer@x.io"); err != nil {
			return err
		}
		if err := tm.WithinTx(ctx, func(inner context.Context) error {
			if inner != ctx {
				t.Error("nested call should receive the same ctx")
			}
			return insertUser(inner, db, "inner@x.io")
		}); err != nil {
			return err
		}
		if outside := countUsers(t, db); outside != 0 {
			t.Errorf("nested call committed early: %d", outside)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if n := countUsers(t, db); n != 0 {
		t.Fatalf("outer rollback must undo nested work, got %d users", n)
	}
}

func TestTxManagerNestedErrorRollsBackOuter(t *testing.T) {
	db := pgtest.New(t)
	tm := postgres.NewTxManager(db.Pool)
	boom := errors.New("inner failed")
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := insertUser(ctx, db, "outer@x.io"); err != nil {
			return err
		}
		return tm.WithinTx(ctx, func(context.Context) error { return boom })
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want inner error, got %v", err)
	}
	if n := countUsers(t, db); n != 0 {
		t.Fatalf("want 0 users, got %d", n)
	}
}

func TestConnOutsideTxUsesPool(t *testing.T) {
	db := pgtest.New(t)
	if err := insertUser(context.Background(), db, "a@x.io"); err != nil {
		t.Fatal(err)
	}
	if n := countUsers(t, db); n != 1 {
		t.Fatalf("want 1, got %d", n)
	}
}
