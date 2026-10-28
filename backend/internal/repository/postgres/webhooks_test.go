package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/notify/webhook"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

var _ service.WebhookEndpointRepository = (*postgres.WebhookRepository)(nil)
var _ service.WebhookOutbox = (*postgres.WebhookRepository)(nil)
var _ webhook.Store = (*postgres.WebhookRepository)(nil)

func (r *repos) webhooks() *postgres.WebhookRepository {
	return postgres.NewWebhookRepository(r.db.Pool, r.tx)
}

func (r *repos) endpoint(t *testing.T, ownerID, url string, active bool) domain.WebhookEndpoint {
	t.Helper()
	e := domain.WebhookEndpoint{ID: newID(t), OwnerID: ownerID, URL: url, Secret: "whsec_" + newID(t), Active: active, CreatedAt: baseTime}
	if err := r.webhooks().Create(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	return e
}

func (r *repos) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := r.db.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWebhookEndpointCRUDIsOwnerScoped(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	a := r.user(t, "a@example.com")
	b := r.user(t, "b@example.com")
	e := r.endpoint(t, a.ID, "http://hook.test/a", true)

	list, err := r.webhooks().ListByOwner(ctx, a.ID)
	if err != nil || len(list) != 1 || list[0].ID != e.ID || list[0].Secret != e.Secret {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if other, _ := r.webhooks().ListByOwner(ctx, b.ID); len(other) != 0 {
		t.Fatalf("other owner sees endpoints: %+v", other)
	}
	if err := r.webhooks().Delete(ctx, b.ID, e.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign delete = %v", err)
	}
	if err := r.webhooks().Delete(ctx, a.ID, "not-a-uuid"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bad id delete = %v", err)
	}
	if err := r.webhooks().Delete(ctx, a.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.webhooks().Delete(ctx, a.ID, e.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestWebhookDeliveryIsAtomicWithEvent(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 1)
	r.endpoint(t, owner.ID, "http://hook.test", true)

	boom := errors.New("boom")
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		ev, err := r.audit.Append(ctx, d.ID, domain.EventDocumentCompleted, "x", nil, baseTime)
		if err != nil {
			return err
		}
		if err := r.webhooks().Enqueue(ctx, ev); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if n := r.count(t, `SELECT count(*) FROM webhook_deliveries`); n != 0 {
		t.Fatalf("rollback left %d deliveries", n)
	}
	if n := r.count(t, `SELECT count(*) FROM audit_events WHERE document_id = $1::uuid`, d.ID); n != 0 {
		t.Fatalf("rollback left %d events", n)
	}

	err = r.tx.WithinTx(ctx, func(ctx context.Context) error {
		ev, err := r.audit.Append(ctx, d.ID, domain.EventDocumentCompleted, "x", nil, baseTime)
		if err != nil {
			return err
		}
		return r.webhooks().Enqueue(ctx, ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := r.count(t, `SELECT count(*) FROM webhook_deliveries`); n != 1 {
		t.Fatalf("deliveries = %d", n)
	}
}

func TestSigningEnqueuesOnlyWebhookEventsForActiveEndpointsOfOwner(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner, d, tokens := r.sent(t, 2)
	stranger := r.user(t, "stranger@example.com")
	mine := r.endpoint(t, owner.ID, "http://hook.test/mine", true)
	r.endpoint(t, owner.ID, "http://hook.test/inactive", false)
	r.endpoint(t, stranger.ID, "http://hook.test/stranger", true)
	svc := r.signing(r.audit).WithOutbox(r.webhooks())

	for _, tok := range tokens {
		if _, err := svc.Approve(ctx, tok, service.ClientMeta{}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.db.Pool.Query(ctx,
		`SELECT a.type, w.endpoint_id::text FROM webhook_deliveries w JOIN audit_events a ON a.id = w.event_id ORDER BY a.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var typ, ep string
		if err := rows.Scan(&typ, &ep); err != nil {
			t.Fatal(err)
		}
		if ep != mine.ID {
			t.Errorf("delivery for unexpected endpoint %s", ep)
		}
		types = append(types, typ)
	}
	want := []string{"signer.approved", "signer.approved", "document.completed"}
	if len(types) != len(want) {
		t.Fatalf("types = %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("types = %v, want %v", types, want)
		}
	}
	if d.ID == "" {
		t.Fatal("no document")
	}
}

func TestRejectEnqueuesDocumentRejectedOnly(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner, _, tokens := r.sent(t, 2)
	r.endpoint(t, owner.ID, "http://hook.test", true)
	svc := r.signing(r.audit).WithOutbox(r.webhooks())

	if _, err := svc.Reject(ctx, tokens[0], "no"); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t, `SELECT count(*) FROM webhook_deliveries w JOIN audit_events a ON a.id = w.event_id WHERE a.type = 'document.rejected'`); n != 1 {
		t.Fatalf("document.rejected deliveries = %d", n)
	}
	if n := r.count(t, `SELECT count(*) FROM webhook_deliveries`); n != 1 {
		t.Fatalf("total deliveries = %d", n)
	}
}

func TestSigningRollbackLeavesNoDelivery(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner, _, tokens := r.sent(t, 1)
	r.endpoint(t, owner.ID, "http://hook.test", true)
	svc := r.signing(r.audit).WithOutbox(failingOutbox{r.webhooks()})

	if _, err := svc.Approve(ctx, tokens[0], service.ClientMeta{}); err == nil {
		t.Fatal("expected failure")
	}
	if n := r.count(t, `SELECT count(*) FROM webhook_deliveries`); n != 0 {
		t.Fatalf("deliveries = %d", n)
	}
	if n := r.count(t, `SELECT count(*) FROM signatures`); n != 0 {
		t.Fatalf("signatures = %d", n)
	}
	if n := r.count(t, `SELECT count(*) FROM audit_events WHERE type = 'signer.approved'`); n != 0 {
		t.Fatalf("approve events = %d", n)
	}
}

type failingOutbox struct{ inner *postgres.WebhookRepository }

func (f failingOutbox) Enqueue(ctx context.Context, ev domain.AuditEvent) error {
	if err := f.inner.Enqueue(ctx, ev); err != nil {
		return err
	}
	if ev.Type == domain.EventDocumentCompleted {
		return errors.New("outbox failure after insert")
	}
	return nil
}

type receiver struct {
	srv     *httptest.Server
	mu      sync.Mutex
	hits    map[string]int
	bodies  map[string][]byte
	headers map[string]http.Header
}

func newReceiver(t *testing.T, status int, delay time.Duration) *receiver {
	t.Helper()
	rc := &receiver{hits: map[string]int{}, bodies: map[string][]byte{}, headers: map[string]http.Header{}}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		time.Sleep(delay)
		key := req.Header.Get(webhook.HeaderDelivery)
		rc.mu.Lock()
		rc.hits[key]++
		rc.bodies[key] = b
		rc.headers[key] = req.Header.Clone()
		rc.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *receiver) total() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	n := 0
	for _, c := range rc.hits {
		n += c
	}
	return n
}

func (r *repos) events(t *testing.T, docID string, n int) []domain.AuditEvent {
	t.Helper()
	var out []domain.AuditEvent
	for i := 0; i < n; i++ {
		ev, err := r.audit.Append(context.Background(), docID, domain.EventSignerApproved, "x", map[string]any{"n": i}, baseTime)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func stopWorker(t *testing.T, w *webhook.Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	})
}

func TestWebhookSuccessMarksDeliveredWithVerifiableSignature(t *testing.T) {
	r := newRepos(t)
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 1)
	rc := newReceiver(t, http.StatusOK, 0)
	ep := r.endpoint(t, owner.ID, rc.srv.URL, true)
	ev := r.events(t, d.ID, 1)[0]
	if err := r.webhooks().Enqueue(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	stopWorker(t, webhook.NewWorker(webhook.Config{Store: r.webhooks(), Workers: 1, PollInterval: 5 * time.Millisecond}))
	waitFor(t, "delivered", func() bool {
		return r.count(t, `SELECT count(*) FROM webhook_deliveries WHERE status = 'delivered' AND attempts = 1`) == 1
	})

	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.hits) != 1 {
		t.Fatalf("hits = %v", rc.hits)
	}
	for key, body := range rc.bodies {
		h := rc.headers[key]
		if h.Get(webhook.HeaderEvent) != "signer.approved" || key != h.Get(webhook.HeaderDelivery) || h.Get(webhook.HeaderTimestamp) == "" {
			t.Errorf("headers = %v", h)
		}
		if !webhook.Verify(ep.Secret, body, h.Get(webhook.HeaderSignature)) {
			t.Errorf("signature does not verify with endpoint secret")
		}
		var p struct {
			ID         string         `json:"id"`
			Type       string         `json:"type"`
			DocumentID string         `json:"document_id"`
			Data       map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatal(err)
		}
		if p.ID != key || p.Type != "signer.approved" || p.DocumentID != d.ID || p.Data["n"] != float64(0) {
			t.Errorf("payload = %+v", p)
		}
	}
}

type atomicClock struct{ ns atomic.Int64 }

func (c *atomicClock) Now() time.Time      { return time.Unix(0, c.ns.Load()).UTC() }
func (c *atomicClock) Set(t time.Time)     { c.ns.Store(t.UnixNano()) }
func (c *atomicClock) Add(d time.Duration) { c.ns.Add(int64(d)) }

func TestWebhookFailureReschedulesWithBackoffAndStopsAtSixAttempts(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 1)
	rc := newReceiver(t, http.StatusInternalServerError, 0)
	r.endpoint(t, owner.ID, rc.srv.URL, true)
	if err := r.webhooks().Enqueue(ctx, r.events(t, d.ID, 1)[0]); err != nil {
		t.Fatal(err)
	}

	clock := &atomicClock{}
	clock.Set(time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond))
	stopWorker(t, webhook.NewWorker(webhook.Config{
		Store: r.webhooks(), Workers: 1, PollInterval: 2 * time.Millisecond,
		Now: clock.Now, Jitter: func(d time.Duration) time.Duration { return d },
	}))

	for attempt := 1; attempt <= webhook.MaxAttempts; attempt++ {
		waitFor(t, "attempt", func() bool {
			return r.count(t, `SELECT count(*) FROM webhook_deliveries WHERE attempts = $1`, attempt) == 1
		})
		var status, lastErr string
		var next time.Time
		if err := r.db.Pool.QueryRow(ctx, `SELECT status, next_attempt_at, coalesce(last_error, '') FROM webhook_deliveries`).Scan(&status, &next, &lastErr); err != nil {
			t.Fatal(err)
		}
		if lastErr == "" {
			t.Errorf("attempt %d: last_error empty", attempt)
		}
		if attempt == webhook.MaxAttempts {
			if status != "failed" {
				t.Fatalf("attempt %d: status = %s", attempt, status)
			}
			break
		}
		want := clock.Now().Add(webhook.Backoff(attempt))
		if status != "pending" || !next.Equal(want) {
			t.Fatalf("attempt %d: status = %s next = %v want %v", attempt, status, next, want)
		}
		time.Sleep(30 * time.Millisecond)
		if got := r.count(t, `SELECT attempts FROM webhook_deliveries`); got != attempt {
			t.Fatalf("delivery retried before it was due: attempts = %d", got)
		}
		clock.Set(next)
	}

	clock.Add(48 * time.Hour)
	time.Sleep(50 * time.Millisecond)
	if got := rc.total(); got != webhook.MaxAttempts {
		t.Fatalf("receiver hits = %d, want %d", got, webhook.MaxAttempts)
	}
	if got := r.count(t, `SELECT attempts FROM webhook_deliveries WHERE status = 'failed'`); got != webhook.MaxAttempts {
		t.Fatalf("attempts after failure = %d", got)
	}
}

func TestWebhookTwoWorkersNeverDoubleDeliver(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 1)
	rc := newReceiver(t, http.StatusOK, 10*time.Millisecond)
	r.endpoint(t, owner.ID, rc.srv.URL, true)
	r.endpoint(t, owner.ID, rc.srv.URL, true)
	const events = 15
	for _, ev := range r.events(t, d.ID, events) {
		if err := r.webhooks().Enqueue(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	const want = events * 2

	for i := 0; i < 2; i++ {
		stopWorker(t, webhook.NewWorker(webhook.Config{Store: r.webhooks(), Workers: 3, PollInterval: 2 * time.Millisecond}))
	}
	waitFor(t, "all delivered", func() bool {
		return r.count(t, `SELECT count(*) FROM webhook_deliveries WHERE status = 'delivered'`) == want
	})
	time.Sleep(50 * time.Millisecond)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.hits) != want {
		t.Fatalf("distinct deliveries = %d, want %d", len(rc.hits), want)
	}
	for key, n := range rc.hits {
		if n != 1 {
			t.Errorf("delivery %s sent %d times", key, n)
		}
	}
	if got := r.count(t, `SELECT count(*) FROM webhook_deliveries WHERE attempts <> 1`); got != 0 {
		t.Errorf("%d deliveries have attempts <> 1", got)
	}
}
