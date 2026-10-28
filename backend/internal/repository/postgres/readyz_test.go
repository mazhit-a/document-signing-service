package postgres_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/repository/postgres/pgtest"
	transport "github.com/mazhit-a/mini-kedo/internal/transport/http"
)

func TestReadyzReflectsDatabaseState(t *testing.T) {
	db := pgtest.New(t)
	h := transport.NewRouter(transport.Options{
		Logger:       slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		MaxBodyBytes: 1024,
		ReadyChecks:  map[string]transport.ReadyCheck{"database": db.Pool.Ping},
	})
	get := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return rec.Code
	}
	if got := get(); got != http.StatusOK {
		t.Fatalf("database up: status %d", got)
	}
	db.Pool.Close()
	if got := get(); got != http.StatusServiceUnavailable {
		t.Fatalf("database down: status %d, want 503", got)
	}
}
