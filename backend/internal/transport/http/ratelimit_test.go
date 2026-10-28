package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

type stubAuth struct{}

func (stubAuth) Register(context.Context, string, string, string) (domain.User, error) {
	return domain.User{}, domain.ErrBadRequest
}

func (stubAuth) Login(context.Context, string, string) (service.LoginResult, error) {
	return service.LoginResult{}, domain.ErrUnauthorized
}

func (stubAuth) Me(context.Context, string) (domain.User, error) {
	return domain.User{}, domain.ErrUnauthorized
}

type stubTokens struct{}

func (stubTokens) Parse(string) (string, error) { return "", domain.ErrUnauthorized }

type stubSigning struct{}

func (stubSigning) View(context.Context, string) (service.SigningView, error) {
	return service.SigningView{}, domain.ErrNotFound
}

func (stubSigning) OpenFile(context.Context, string) (domain.Document, io.ReadCloser, error) {
	return domain.Document{}, nil, domain.ErrNotFound
}

func (stubSigning) Approve(context.Context, string, service.ClientMeta) (service.DecisionResult, error) {
	return service.DecisionResult{}, domain.ErrNotFound
}

func (stubSigning) Reject(context.Context, string, string) (service.DecisionResult, error) {
	return service.DecisionResult{}, domain.ErrNotFound
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func TestLimiterBurstAndRefill(t *testing.T) {
	clk := newClock()
	l := NewLimiter(3, 10*time.Second, clk.Now)

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d within burst denied", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok {
		t.Fatal("request over burst allowed")
	}
	if wait <= 0 || wait > 10*time.Second {
		t.Fatalf("wait = %v", wait)
	}

	clk.Advance(5 * time.Second)
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("allowed before a full token refilled")
	}
	clk.Advance(5 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("denied after a token refilled")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("only one token should have refilled")
	}

	clk.Advance(time.Hour)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("refill after long idle should restore burst, request %d denied", i)
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("refill must be capped at burst")
	}
}

func TestLimiterPerKeyIsolation(t *testing.T) {
	clk := newClock()
	l := NewLimiter(1, time.Minute, clk.Now)
	if ok, _ := l.Allow("1.1.1.1"); !ok {
		t.Fatal("first denied")
	}
	if ok, _ := l.Allow("1.1.1.1"); ok {
		t.Fatal("second allowed")
	}
	if ok, _ := l.Allow("2.2.2.2"); !ok {
		t.Fatal("other key affected by first key")
	}
}

func TestLimiterSweepsIdleBuckets(t *testing.T) {
	clk := newClock()
	l := NewLimiter(1, time.Second, clk.Now)
	for i := 0; i < bucketSweepEvery-1; i++ {
		l.Allow(strconv.Itoa(i))
	}
	clk.Advance(time.Minute)
	l.Allow("trigger")
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n > 1 {
		t.Fatalf("idle buckets not swept: %d", n)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	clk := newClock()
	l := NewLimiter(2, time.Minute, clk.Now)
	h := RateLimit(l, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	do := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 2; i++ {
		if rec := do("10.0.0.1:1111"); rec.Code != 200 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	rec := do("10.0.0.1:2222")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	if ra, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || ra < 1 {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	var got envelopeResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code != "rate_limited" {
		t.Fatalf("code %q", got.Error.Code)
	}
	if rec := do("10.0.0.2:1111"); rec.Code != 200 {
		t.Fatalf("other IP limited: %d", rec.Code)
	}
}

func TestRateLimitNilLimiterPassesThrough(t *testing.T) {
	h := RateLimit(nil, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		xff        string
		trustProxy bool
		want       string
	}{
		{"remote addr", "9.9.9.9:5000", "", false, "9.9.9.9"},
		{"xff ignored when untrusted", "9.9.9.9:5000", "1.2.3.4", false, "9.9.9.9"},
		{"xff last hop when trusted", "9.9.9.9:5000", "6.6.6.6, 1.2.3.4", true, "1.2.3.4"},
		{"bad xff falls back", "9.9.9.9:5000", "garbage", true, "9.9.9.9"},
		{"trusted without xff", "9.9.9.9:5000", "", true, "9.9.9.9"},
		{"ipv6", "[2001:db8::1]:5000", "", false, "2001:db8::1"},
		{"no port", "9.9.9.9", "", false, "9.9.9.9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := ClientIP(req, tc.trustProxy); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRouterRateLimitsLoginAndSignOnly(t *testing.T) {
	clk := newClock()
	h := NewRouter(Options{
		Logger:       testLogger(new(bytes.Buffer)),
		MaxBodyBytes: 1024,
		Auth:         stubAuth{},
		Tokens:       stubTokens{},
		Signing:      stubSigning{},
		LoginLimiter: NewLimiter(1, time.Hour, clk.Now),
		SignLimiter:  NewLimiter(1, time.Hour, clk.Now),
	})
	do := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "10.0.0.1:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	do("POST", "/api/v1/auth/login")
	if got := do("POST", "/api/v1/auth/login"); got != 429 {
		t.Fatalf("login second: %d", got)
	}
	do("GET", "/api/v1/sign/abc")
	if got := do("GET", "/api/v1/sign/abc"); got != 429 {
		t.Fatalf("sign second: %d", got)
	}
	for i := 0; i < 3; i++ {
		if got := do("POST", "/api/v1/auth/register"); got == 429 {
			t.Fatal("register must not be limited")
		}
		if got := do("GET", "/healthz"); got != 200 {
			t.Fatalf("healthz: %d", got)
		}
	}
}
