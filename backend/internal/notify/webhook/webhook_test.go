package webhook_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/notify/webhook"
)

func TestSignVerify(t *testing.T) {
	body := []byte(`{"a":1}`)
	sig := webhook.Sign("secret", body)
	cases := []struct {
		name   string
		secret string
		body   []byte
		sig    string
		want   bool
	}{
		{"valid", "secret", body, sig, true},
		{"wrong secret", "other", body, sig, false},
		{"tampered body", "secret", []byte(`{"a":2}`), sig, false},
		{"missing prefix", "secret", body, sig[len("sha256="):], false},
		{"not hex", "secret", body, "sha256=zz", false},
		{"empty", "secret", body, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := webhook.Verify(c.secret, c.body, c.sig); got != c.want {
				t.Fatalf("Verify = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBackoffSchedule(t *testing.T) {
	want := []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		if got := webhook.Backoff(i + 1); got != w {
			t.Errorf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestRandomJitterStaysWithinBounds(t *testing.T) {
	base := time.Minute
	for i := 0; i < 1000; i++ {
		got := webhook.RandomJitter(base)
		if got < 48*time.Second || got > 72*time.Second {
			t.Fatalf("jitter out of bounds: %v", got)
		}
	}
}

type memStore struct {
	mu      sync.Mutex
	d       webhook.Delivery
	status  webhook.Status
	outcome webhook.Outcome
	calls   int
	oneShot bool
	armed   bool
}

func (s *memStore) ProcessNext(ctx context.Context, _ time.Time, handle func(context.Context, webhook.Delivery) (webhook.Outcome, error)) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != webhook.StatusPending || (s.oneShot && !s.armed) {
		return false, nil
	}
	s.armed = false
	out, err := handle(ctx, s.d)
	if err != nil {
		return false, err
	}
	s.d.Attempts++
	s.status = out.Status
	s.outcome = out
	s.calls++
	return true, nil
}

func newMem(url string) *memStore {
	return &memStore{status: webhook.StatusPending, d: webhook.Delivery{
		ID: "d1", IdempotencyKey: "7:e1", URL: url, Secret: "topsecret", EventType: "document.completed",
		DocumentID: "doc-1", EventData: map[string]any{"signer_count": 2}, EventAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}
}

func runUntil(t *testing.T, w *webhook.Worker, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { w.Run(ctx); close(finished) }()
	deadline := time.After(5 * time.Second)
	for !done() {
		select {
		case <-deadline:
			cancel()
			t.Fatal("timed out waiting for worker")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after cancel")
	}
}

func TestWorkerSendsSignedRequest(t *testing.T) {
	var mu sync.Mutex
	var gotHeaders http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotHeaders, gotBody = r.Header.Clone(), b
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	store := newMem(srv.URL)
	w := webhook.NewWorker(webhook.Config{Store: store, Workers: 1, PollInterval: time.Millisecond})
	runUntil(t, w, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return store.status == webhook.StatusDelivered })

	mu.Lock()
	defer mu.Unlock()
	if gotHeaders.Get(webhook.HeaderEvent) != "document.completed" || gotHeaders.Get(webhook.HeaderDelivery) != "7:e1" {
		t.Errorf("headers = %v", gotHeaders)
	}
	if gotHeaders.Get(webhook.HeaderTimestamp) == "" {
		t.Error("missing timestamp header")
	}
	if !webhook.Verify("topsecret", gotBody, gotHeaders.Get(webhook.HeaderSignature)) {
		t.Errorf("signature does not verify for body %s", gotBody)
	}
	if webhook.Verify("wrong", gotBody, gotHeaders.Get(webhook.HeaderSignature)) {
		t.Error("signature verifies with the wrong secret")
	}
}

func TestWorkerRetriesWithBackoffAndStopsAtMaxAttempts(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fixed := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newMem(srv.URL)
	store.oneShot = true
	w := webhook.NewWorker(webhook.Config{
		Store: store, Workers: 1, PollInterval: time.Millisecond,
		Now: func() time.Time { return fixed }, Jitter: func(d time.Duration) time.Duration { return d },
	})

	var wantNext []time.Time
	for i := 1; i < webhook.MaxAttempts; i++ {
		wantNext = append(wantNext, fixed.Add(webhook.Backoff(i)))
	}
	seen := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for attempt := 1; attempt <= webhook.MaxAttempts; attempt++ {
		store.mu.Lock()
		store.status = webhook.StatusPending
		store.armed = true
		store.mu.Unlock()
		single, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { w.Run(single); close(done) }()
		for {
			store.mu.Lock()
			c := store.calls
			store.mu.Unlock()
			if c == attempt {
				break
			}
			time.Sleep(time.Millisecond)
		}
		stop()
		<-done

		store.mu.Lock()
		out := store.outcome
		store.mu.Unlock()
		if attempt < webhook.MaxAttempts {
			if out.Status != webhook.StatusPending || !out.NextAttemptAt.Equal(wantNext[attempt-1]) {
				t.Fatalf("attempt %d outcome = %+v, want pending at %v", attempt, out, wantNext[attempt-1])
			}
			seen++
		} else if out.Status != webhook.StatusFailed {
			t.Fatalf("attempt %d outcome = %+v, want failed", attempt, out)
		}
		if out.Error == "" {
			t.Errorf("attempt %d has no error recorded", attempt)
		}
	}
	if hits.Load() != webhook.MaxAttempts || seen != webhook.MaxAttempts-1 {
		t.Fatalf("hits = %d seen = %d", hits.Load(), seen)
	}
}

func TestWorkerDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("redirect target must not be called")
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	store := newMem(srv.URL)
	store.oneShot, store.armed = true, true
	w := webhook.NewWorker(webhook.Config{Store: store, Workers: 1, PollInterval: time.Millisecond})
	runUntil(t, w, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return store.calls >= 1 })
	if store.outcome.Status != webhook.StatusPending {
		t.Fatalf("outcome = %+v", store.outcome)
	}
}

func TestWorkerStopsOnContextCancelDuringRequest(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	store := newMem(srv.URL)
	w := webhook.NewWorker(webhook.Config{Store: store, Workers: 3, PollInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { w.Run(ctx); close(finished) }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request never started")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after cancel")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.calls != 0 || store.status != webhook.StatusPending {
		t.Fatalf("interrupted delivery was recorded: calls=%d status=%s", store.calls, store.status)
	}
}

func TestWorkerWithZeroWorkersReturnsImmediately(t *testing.T) {
	w := webhook.NewWorker(webhook.Config{Store: newMem("http://unused.invalid"), Workers: 0})
	done := make(chan struct{})
	go func() { w.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return")
	}
}
