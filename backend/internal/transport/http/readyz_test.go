package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func readyRouter(buf *bytes.Buffer, checks map[string]ReadyCheck) http.Handler {
	return NewRouter(Options{Logger: testLogger(buf), MaxBodyBytes: 1024, ReadyChecks: checks})
}

func TestReadyzOK(t *testing.T) {
	var buf bytes.Buffer
	h := readyRouter(&buf, map[string]ReadyCheck{
		"database": func(context.Context) error { return nil },
		"storage":  func(context.Context) error { return nil },
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Checks["database"] != "ok" || body.Checks["storage"] != "ok" {
		t.Fatalf("body %+v", body)
	}
}

func TestReadyz503WhenCheckFailsAndDoesNotLeak(t *testing.T) {
	for _, failing := range []string{"database", "storage"} {
		t.Run(failing, func(t *testing.T) {
			var buf bytes.Buffer
			checks := map[string]ReadyCheck{
				"database": func(context.Context) error { return nil },
				"storage":  func(context.Context) error { return nil },
			}
			checks[failing] = func(context.Context) error {
				return errors.New("dial tcp 10.1.2.3:5432: connection refused password=hunter2")
			}
			h := readyRouter(&buf, checks)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want 503", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "10.1.2.3") || strings.Contains(rec.Body.String(), "hunter2") {
				t.Fatalf("leaked error: %s", rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"`+failing+`":"unavailable"`) {
				t.Fatalf("body %s", rec.Body.String())
			}
			if !strings.Contains(buf.String(), "readiness check failed") {
				t.Fatal("failure not logged")
			}
		})
	}
}
