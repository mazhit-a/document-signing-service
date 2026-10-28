package http

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func newTestRouter(buf *bytes.Buffer, extra func(*http.ServeMux)) http.Handler {
	// Reuse the real chain by wrapping a custom mux the same way NewRouter does.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	if extra != nil {
		extra(mux)
	}
	var h http.Handler = mux
	h = BodyLimit(16)(h)
	h = CORS("http://allowed.example")(h)
	h = Recover(h)
	h = AccessLog(h)
	h = RequestID(testLogger(buf))(h)
	return h
}

func TestHealthzAndRequestID(t *testing.T) {
	var buf bytes.Buffer
	h := NewRouter(Options{Logger: testLogger(&buf), CORSAllowedOrigin: "http://x", MaxBodyBytes: 1024})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	id := rec.Header().Get("X-Request-ID")
	if id == "" {
		t.Fatal("missing X-Request-ID header")
	}
	lines := logLines(t, &buf)
	if len(lines) == 0 {
		t.Fatal("no log lines")
	}
	for _, l := range lines {
		if l["request_id"] != id {
			t.Fatalf("log line lacks request_id %q: %v", id, l)
		}
	}
}

func TestInboundRequestID(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, nil)
	for _, tt := range []struct {
		in   string
		same bool
	}{{"abcd-1234-efgh", true}, {"bad id with spaces", false}, {"x", false}} {
		req := httptest.NewRequest("GET", "/healthz", nil)
		req.Header.Set("X-Request-ID", tt.in)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("X-Request-ID"); (got == tt.in) != tt.same {
			t.Errorf("in=%q got=%q", tt.in, got)
		}
	}
}

func TestPanicYields500Internal(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, func(m *http.ServeMux) {
		m.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("secret detail") })
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/boom", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "internal" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if strings.Contains(rec.Body.String(), "secret detail") {
		t.Fatal("panic value leaked to client")
	}
	// access log must still record 500 with request_id
	var sawAccess bool
	for _, l := range logLines(t, &buf) {
		if l["request_id"] == nil {
			t.Fatalf("missing request_id: %v", l)
		}
		if l["msg"] == "request" && l["status"] == float64(500) {
			sawAccess = true
		}
	}
	if !sawAccess {
		t.Fatal("access log did not record status 500")
	}
}

func TestTokenRedactedInLogs(t *testing.T) {
	const token = "SUPERSECRETTOKEN123"
	var buf bytes.Buffer
	h := newTestRouter(&buf, func(m *http.ServeMux) {
		m.HandleFunc("POST /sign/{token}/approve", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		m.HandleFunc("GET /sign/{token}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	})
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/sign/" + token, "/sign/[redacted]"},
		{"POST", "/sign/" + token + "/approve", "/sign/[redacted]/approve"},
	} {
		buf.Reset()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(c.method, c.path, nil))
		if strings.Contains(buf.String(), token) {
			t.Fatalf("token leaked in log: %s", buf.String())
		}
		lines := logLines(t, &buf)
		if len(lines) != 1 || lines[0]["path"] != c.want {
			t.Fatalf("path = %v, want %q", lines, c.want)
		}
	}
}

func TestRedactPath(t *testing.T) {
	for in, want := range map[string]string{
		"/healthz":              "/healthz",
		"/api/v1/documents/abc": "/api/v1/documents/abc",
		"/api/v1/sign/tok":      "/api/v1/sign/[redacted]",
		"/api/v1/sign/tok/file": "/api/v1/sign/[redacted]/file",
	} {
		if got := RedactPath(in); got != want {
			t.Errorf("RedactPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCORS(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, nil)

	t.Run("allowed origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/healthz", nil)
		req.Header.Set("Origin", "http://allowed.example")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Header().Get("Access-Control-Allow-Origin") != "http://allowed.example" {
			t.Fatal("origin not allowed")
		}
	})
	t.Run("other origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/healthz", nil)
		req.Header.Set("Origin", "http://evil.example")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("foreign origin was allowed")
		}
	})
	t.Run("preflight", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/healthz", nil)
		req.Header.Set("Origin", "http://allowed.example")
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("preflight status = %d", rec.Code)
		}
	})
}

func TestBodyLimit(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, func(m *http.ServeMux) {
		m.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.ReadAll(r.Body); err != nil {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
	})
	for _, c := range []struct {
		size, want int
	}{{10, 200}, {16, 200}, {17, 413}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/echo", strings.NewReader(strings.Repeat("a", c.size))))
		if rec.Code != c.want {
			t.Errorf("size %d: status %d, want %d", c.size, rec.Code, c.want)
		}
	}
}
