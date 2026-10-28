package email_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/notify/email"
	"github.com/mazhit-a/mini-kedo/internal/notify/email/smtptest"
)

type memRow struct {
	item   email.Item
	status email.Status
	next   time.Time
	lastEr string
	busy   bool
}

type memStore struct {
	mu   sync.Mutex
	rows []*memRow
}

func (m *memStore) add(t *testing.T, c *email.Crypto, id string, kind email.Kind, to string, p email.Payload) *memRow {
	t.Helper()
	plain, _ := json.Marshal(p)
	sealed, err := c.Seal(id, kind, plain)
	if err != nil {
		t.Fatal(err)
	}
	r := &memRow{item: email.Item{ID: id, Kind: kind, Recipient: to, Ciphertext: sealed}, status: email.StatusPending}
	m.mu.Lock()
	m.rows = append(m.rows, r)
	m.mu.Unlock()
	return r
}

func (m *memStore) ProcessNext(ctx context.Context, now time.Time, handle func(ctx context.Context, it email.Item) (email.Outcome, error)) (bool, error) {
	m.mu.Lock()
	var row *memRow
	for _, r := range m.rows {
		if r.status == email.StatusPending && !r.busy && !r.next.After(now) {
			row = r
			r.busy = true
			break
		}
	}
	m.mu.Unlock()
	if row == nil {
		return false, nil
	}
	out, err := handle(ctx, row.item)
	m.mu.Lock()
	defer m.mu.Unlock()
	row.busy = false
	if err != nil {
		return false, err
	}
	row.item.Attempts++
	row.status, row.lastEr = out.Status, out.Error
	if out.Status == email.StatusPending {
		row.next = out.NextAttemptAt
	} else {
		row.item.Ciphertext = nil
	}
	return true, nil
}

type fakeSender struct {
	mu    sync.Mutex
	sent  []email.Message
	err   error
	block chan struct{}
}

func (f *fakeSender) Send(ctx context.Context, m email.Message) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
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

func newTestWorker(store email.Store, sender email.Sender, c *email.Crypto, clock *fakeClock) *email.Worker {
	return email.NewWorker(email.WorkerConfig{
		Store: store, Sender: sender, Crypto: c, Workers: 1, PollInterval: time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: clock.Now,
		Jitter: func(d time.Duration) time.Duration { return d },
	})
}

func runOnce(t *testing.T, w *email.Worker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	w.Run(ctx)
}

func TestWorkerSendsAndMarksSent(t *testing.T) {
	c := mustCrypto(t, keyA)
	store := &memStore{}
	row := store.add(t, c, "r1", email.KindSignerTurn, "a@x.kz", samplePayload)
	sender := &fakeSender{}
	runOnce(t, newTestWorker(store, sender, c, &fakeClock{t: time.Unix(1_800_000_000, 0).UTC()}))

	if row.status != email.StatusSent || row.item.Attempts != 1 || row.item.Ciphertext != nil {
		t.Fatalf("row = %+v", row)
	}
	if len(sender.sent) != 1 || sender.sent[0].To != "a@x.kz" {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestWorkerRetriesWithBackoffThenFails(t *testing.T) {
	c := mustCrypto(t, keyA)
	store := &memStore{}
	row := store.add(t, c, "r1", email.KindSignerTurn, "a@x.kz", samplePayload)
	sender := &fakeSender{err: errors.New("451 try later")}
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0).UTC()}
	w := newTestWorker(store, sender, c, clock)

	wantDelays := []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}
	for i, delay := range wantDelays {
		start := clock.Now()
		runOnce(t, w)
		if row.status != email.StatusPending || row.item.Attempts != i+1 || !row.next.Equal(start.Add(delay)) {
			t.Fatalf("after attempt %d: status=%s attempts=%d next=%v (want %v)", i+1, row.status, row.item.Attempts, row.next, start.Add(delay))
		}
		if row.item.Ciphertext == nil || row.lastEr != "451 try later" {
			t.Fatalf("pending row must keep payload and error: %+v", row)
		}
		runOnce(t, w)
		if row.item.Attempts != i+1 {
			t.Fatalf("row retried before it was due (attempts %d)", row.item.Attempts)
		}
		clock.Advance(delay)
	}
	runOnce(t, w)
	if row.status != email.StatusFailed || row.item.Attempts != email.MaxAttempts || row.item.Ciphertext != nil {
		t.Fatalf("final row = %+v", row)
	}
	clock.Advance(24 * time.Hour)
	runOnce(t, w)
	if row.item.Attempts != email.MaxAttempts || len(sender.sent) != 0 {
		t.Error("failed row must not be retried")
	}
}

func TestWorkerFailsPermanentlyOnBadPayloadOrHeader(t *testing.T) {
	c := mustCrypto(t, keyA)
	store := &memStore{}
	undecryptable := store.add(t, mustCrypto(t, keyB), "r1", email.KindSignerTurn, "a@x.kz", samplePayload)
	wrongAAD := store.add(t, c, "r2", email.KindSignerTurn, "a@x.kz", samplePayload)
	wrongAAD.item.ID = "r2-moved"
	badKind := store.add(t, c, "r3", "weird", "a@x.kz", samplePayload)
	sender := &fakeSender{}
	runOnce(t, newTestWorker(store, sender, c, &fakeClock{t: time.Unix(1_800_000_000, 0).UTC()}))

	for name, r := range map[string]*memRow{"wrong key": undecryptable, "moved row": wrongAAD, "bad kind": badKind} {
		if r.status != email.StatusFailed || r.item.Attempts != 1 || r.lastEr == "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if len(sender.sent) != 0 {
		t.Error("nothing may be sent")
	}

	store2 := &memStore{}
	inj := store2.add(t, c, "r4", email.KindSignerTurn, "a@x.kz\r\nBcc: e@x.io", samplePayload)
	real := newSender(t, smtptest.New(t, smtptest.Options{User: smtpUser, Password: smtpPass}), nil)
	runOnce(t, newTestWorker(store2, real, c, &fakeClock{t: time.Unix(1_800_000_000, 0).UTC()}))
	if inj.status != email.StatusFailed {
		t.Errorf("header injection must fail permanently: %+v", inj)
	}
}

func TestWorkerStopsOnContextCancelDuringSend(t *testing.T) {
	c := mustCrypto(t, keyA)
	store := &memStore{}
	row := store.add(t, c, "r1", email.KindSignerTurn, "a@x.kz", samplePayload)
	sender := &fakeSender{block: make(chan struct{})}
	w := newTestWorker(store, sender, c, &fakeClock{t: time.Unix(1_800_000_000, 0).UTC()})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop on cancel")
	}
	if row.status != email.StatusPending || row.item.Attempts != 0 {
		t.Errorf("interrupted attempt must not be recorded: %+v", row)
	}
}

func TestWorkerWithZeroWorkersReturnsImmediately(t *testing.T) {
	w := email.NewWorker(email.WorkerConfig{Store: &memStore{}, Workers: 0})
	done := make(chan struct{})
	go func() {
		w.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with zero workers blocked")
	}
}
