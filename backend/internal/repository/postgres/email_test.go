package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/notify/email"
	"github.com/mazhit-a/mini-kedo/internal/notify/email/smtptest"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

const (
	mailUser = "mailer@kedo.test"
	mailPass = "very-secret-smtp-password"
)

func (r *repos) mailer(t *testing.T, srv *smtptest.Server) *email.SMTPSender {
	t.Helper()
	s, err := email.NewSMTPSender(email.SMTPConfig{
		Host: srv.Host(), Port: srv.Port(), User: mailUser, Password: mailPass,
		From: "noreply@kedo.test", TLSConfig: srv.TLSConfig(), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (r *repos) emailWorker(sender email.Sender, workers int, logger *slog.Logger, clock *atomicClock) *email.Worker {
	cfg := email.WorkerConfig{
		Store: r.email, Sender: sender, Crypto: r.crypto, Workers: workers, PollInterval: 2 * time.Millisecond, Logger: logger,
	}
	if clock != nil {
		cfg.Now = clock.Now
		cfg.Jitter = func(d time.Duration) time.Duration { return d }
	}
	return email.NewWorker(cfg)
}

func startEmailWorker(t *testing.T, w *email.Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("email worker did not stop")
		}
	})
}

func (r *repos) enqueueMail(t *testing.T, docID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		err := r.email.Enqueue(context.Background(), email.Outgoing{
			DocumentID: docID, Kind: email.KindSignerTurn, Recipient: fmt.Sprintf("signer%02d@example.com", i),
			Payload: email.Payload{
				DocumentTitle: "Contract", OwnerName: "Owner", OwnerEmail: "o@example.com", SignerName: "S", Position: 1, Total: 1,
				Link: fmt.Sprintf("http://web.test/sign/token-%02d", i),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func lastLine(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func TestEmailOutboxSchema(t *testing.T) {
	r := newRepos(t)
	var def string
	err := r.db.Pool.QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_email_outbox_due'`).Scan(&def)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "next_attempt_at") || !strings.Contains(def, "WHERE") || !strings.Contains(def, "pending") {
		t.Fatalf("index is not partial on pending: %s", def)
	}
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 1)
	for _, q := range []string{
		`INSERT INTO email_outbox (document_id, kind, recipient) VALUES ($1::uuid, 'bogus', 'a@x.io')`,
		`INSERT INTO email_outbox (document_id, kind, recipient, status) VALUES ($1::uuid, 'signer_turn', 'a@x.io', 'bogus')`,
	} {
		if _, err := r.db.Pool.Exec(context.Background(), q, d.ID); pgCode(err) != "23514" {
			t.Errorf("%s: want check violation, got %v", q, err)
		}
	}
}

func TestNoRawTokenOrLinkAtRest(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	_, _, tokens := r.sent(t, 3)
	svc := r.signing(r.audit)
	if _, err := svc.Approve(ctx, tokens.get(1), service.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	second := tokens.get(2)
	if second == "" {
		t.Fatal("signer 2 has no token after approve")
	}
	if _, err := svc.Approve(ctx, second, service.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	third := tokens.get(3)
	raw := []string{tokens.get(1), second, third}

	for _, table := range []string{"signers", "email_outbox"} {
		rows, err := r.db.Pool.Query(ctx, fmt.Sprintf(`SELECT row_to_json(t)::text FROM %s t`, table))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			n++
			if strings.Contains(row, "/sign/") {
				t.Errorf("%s row contains /sign/: %s", table, row)
			}
			for _, tok := range raw {
				if strings.Contains(row, tok) {
					t.Errorf("%s row contains a raw token", table)
				}
			}
		}
		rows.Close()
		if n == 0 {
			t.Fatalf("%s is empty", table)
		}
	}

	cts, err := r.db.Pool.Query(ctx, `SELECT payload_ciphertext FROM email_outbox WHERE payload_ciphertext IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer cts.Close()
	count := 0
	for cts.Next() {
		var ct []byte
		if err := cts.Scan(&ct); err != nil {
			t.Fatal(err)
		}
		count++
		if bytes.Contains(ct, []byte("/sign/")) || bytes.Contains(ct, []byte("http://web.test")) {
			t.Error("ciphertext contains plaintext link")
		}
		for _, tok := range raw {
			if bytes.Contains(ct, []byte(tok)) {
				t.Error("ciphertext contains plaintext token")
			}
		}
	}
	if count != 3 {
		t.Errorf("ciphertext rows = %d, want 3 signer_turn rows", count)
	}
}

func TestIssueOnTurnStoresHashesAndOutboxRows(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	_, d, tokens := r.sent(t, 3)
	svc := r.signing(r.audit)

	if n := r.countWhere(t, `SELECT count(*) FROM signers WHERE document_id = $1::uuid AND token_hash IS NOT NULL`, d.ID); n != 1 {
		t.Fatalf("hashes after send = %d, want 1", n)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE document_id = $1::uuid AND kind = 'signer_turn'`, d.ID); n != 1 {
		t.Fatalf("signer_turn rows after send = %d", n)
	}
	if _, err := svc.Approve(ctx, tokens.get(1), service.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM signers WHERE document_id = $1::uuid AND token_hash IS NOT NULL`, d.ID); n != 2 {
		t.Fatalf("hashes after first approve = %d, want 2", n)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE document_id = $1::uuid AND kind = 'signer_turn'`, d.ID); n != 2 {
		t.Fatalf("signer_turn rows = %d", n)
	}
	got, _ := r.documents.GetByID(ctx, d.ID)
	if got.Signers[2].TokenHash != "" || got.Signers[2].Status != domain.SignerWaiting {
		t.Errorf("signer 3 = %+v", got.Signers[2])
	}
	view, err := svc.View(ctx, tokens.get(2))
	if err != nil || !view.CanAct || view.Signer.Order != 2 {
		t.Errorf("view = %+v, %v", view, err)
	}
	if _, err := svc.Approve(ctx, tokens.get(1), service.ClientMeta{}); err != nil {
		t.Errorf("old token of finished signer must stay idempotent: %v", err)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE document_id = $1::uuid`, d.ID); n != 2 {
		t.Errorf("repeat approve added outbox rows: %d", n)
	}
}

type failAfterInsert struct {
	inner service.EmailOutbox
	kind  email.Kind
}

func (f failAfterInsert) Enqueue(ctx context.Context, m email.Outgoing) error {
	if err := f.inner.Enqueue(ctx, m); err != nil {
		return err
	}
	if m.Kind == f.kind {
		return errors.New("forced failure after outbox insert")
	}
	return nil
}

func TestOutboxFailureRollsBackSignatureAndRow(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()

	_, d, tokens := r.sent(t, 2)
	before := r.countWhere(t, `SELECT count(*) FROM email_outbox`)
	svc := service.NewSigningService(r.documents, r.signers, r.signatures, r.audit, nil, r.tx, service.SystemClock{}, service.UUIDGenerator{}, notify.Noop{}, testSealSecret).
		WithEmail(failAfterInsert{r.email, email.KindSignerTurn}, r.users, "http://web.test")
	if _, err := svc.Approve(ctx, tokens.get(1), service.ClientMeta{}); err == nil {
		t.Fatal("expected failure")
	}
	if n := r.countWhere(t, `SELECT count(*) FROM signatures WHERE document_id = $1::uuid`, d.ID); n != 0 {
		t.Errorf("signature survived rollback: %d", n)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox`); n != before {
		t.Errorf("outbox rows = %d, want %d", n, before)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM signers WHERE document_id = $1::uuid AND token_hash IS NOT NULL`, d.ID); n != 1 {
		t.Errorf("next signer token survived rollback: %d hashes", n)
	}
	got, _ := r.documents.GetByID(ctx, d.ID)
	if got.Signers[0].Status != domain.SignerPending || got.Signers[1].Status != domain.SignerWaiting {
		t.Errorf("partial state: %+v", got.Signers)
	}

	owner := r.user(t, "o2@example.com")
	draft := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 2)
	life := service.NewLifecycleService(r.documents, r.signers, r.audit, r.tx, service.SystemClock{}, notify.Noop{}, "http://web.test").
		WithEmail(failAfterInsert{r.email, email.KindSignerTurn}, r.users)
	if _, err := life.SendDocument(ctx, owner.ID, draft.ID); err == nil {
		t.Fatal("expected send failure")
	}
	got, _ = r.documents.GetByID(ctx, draft.ID)
	if got.Status != domain.DocumentDraft || got.Signers[0].TokenHash != "" {
		t.Errorf("send leaked partial state: %+v", got)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE document_id = $1::uuid`, draft.ID); n != 0 {
		t.Errorf("outbox rows for rolled back send = %d", n)
	}
}

func TestWorkerDeliversLinkThatResolvesAndWipesPayload(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	_, _, tokens := r.sent(t, 2)
	srv := smtptest.New(t, smtptest.Options{User: mailUser, Password: mailPass})
	startEmailWorker(t, r.emailWorker(r.mailer(t, srv), 1, nil, nil))

	waitFor(t, "first email sent", func() bool {
		return r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE status = 'sent'`) == 1
	})
	msgs := srv.Messages()
	if len(msgs) != 1 || msgs[0].To[0] != "s1@example.com" || !strings.HasPrefix(msgs[0].Subject(), "Please sign: Contract") {
		t.Fatalf("messages = %+v", msgs)
	}
	link := lastLine(msgs[0].Text(), "http://web.test/sign/")
	token := strings.TrimPrefix(link, "http://web.test/sign/")
	if token != tokens.get(1) {
		t.Fatalf("emailed token does not match issued token")
	}
	view, err := r.signing(r.audit).View(ctx, token)
	if err != nil || view.Signer.Order != 1 {
		t.Fatalf("emailed link does not resolve: %+v %v", view, err)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE status = 'sent' AND payload_ciphertext IS NULL AND sent_at IS NOT NULL AND attempts = 1`); n != 1 {
		t.Errorf("sent row must have payload wiped, sent_at set, attempts 1")
	}

	if _, err := r.signing(r.audit).Approve(ctx, token, service.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second email sent", func() bool { return len(srv.Messages()) == 2 })
	second := lastLine(srv.Messages()[1].Text(), "http://web.test/sign/")
	if _, err := r.signing(r.audit).View(ctx, strings.TrimPrefix(second, "http://web.test/sign/")); err != nil {
		t.Errorf("second emailed link does not resolve: %v", err)
	}
}

func TestRejectedAndCompletedOwnerEmails(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	srv := smtptest.New(t, smtptest.Options{User: mailUser, Password: mailPass})
	startEmailWorker(t, r.emailWorker(r.mailer(t, srv), 1, nil, nil))

	_, _, tokens := r.sent(t, 1)
	if _, err := r.signing(r.audit).Approve(ctx, tokens.get(1), service.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	_, _, tokens2 := r.sentFor(t, "o2@example.com", 1)
	if _, err := r.signing(r.audit).Reject(ctx, tokens2.get(1), "Wrong salary amount"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "four emails", func() bool { return len(srv.Messages()) == 4 })

	subjects := map[string]string{}
	for _, m := range srv.Messages() {
		subjects[strings.SplitN(m.Subject(), ":", 2)[0]] = m.Text()
	}
	if !strings.Contains(subjects["Completed"], "document is completed") {
		t.Errorf("completed email missing: %v", subjects)
	}
	rej := subjects["Rejected"]
	if !strings.Contains(rej, "Wrong salary amount") || !strings.Contains(rej, "s1@example.com") {
		t.Errorf("rejected email = %q", rej)
	}
}

func (r *repos) sentFor(t *testing.T, ownerEmail string, signers int) (domain.User, domain.Document, tokenSet) {
	t.Helper()
	owner := r.user(t, ownerEmail)
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, signers)
	res, err := r.lifecycle(r.audit).SendDocument(context.Background(), owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return owner, d, tokenSet{r: r, docID: d.ID, first: strings.TrimPrefix(res.Links[0].URL, "http://web.test/sign/")}
}

func TestSMTPFailureNeverRollsBackSigning(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	_, d, tokens := r.sent(t, 2)
	srv := smtptest.New(t, smtptest.Options{User: mailUser, Password: mailPass, FailStage: "END", FailReply: "451 4.3.0 try again later"})
	startEmailWorker(t, r.emailWorker(r.mailer(t, srv), 1, nil, nil))

	if _, err := r.signing(r.audit).Approve(ctx, tokens.get(1), service.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "failed attempt recorded", func() bool {
		return r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE attempts >= 1`) >= 1
	})
	srv.Close()
	if _, err := r.signing(r.audit).Approve(ctx, tokens.get(2), service.ClientMeta{}); err != nil {
		t.Fatalf("approve must succeed with SMTP down: %v", err)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM signatures WHERE document_id = $1::uuid`, d.ID); n != 2 {
		t.Errorf("signatures = %d", n)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE status = 'pending' AND payload_ciphertext IS NOT NULL`); n < 2 {
		t.Errorf("undelivered rows must stay pending with payload, got %d", n)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE status = 'sent'`); n != 0 {
		t.Errorf("nothing can have been sent, got %d", n)
	}
	got, _ := r.documents.GetByID(ctx, d.ID)
	if got.Status != domain.DocumentCompleted {
		t.Errorf("status = %s", got.Status)
	}

	_, d2, tokens2 := r.sentFor(t, "o3@example.com", 1)
	if _, err := r.signing(r.audit).Reject(ctx, tokens2.get(1), "no"); err != nil {
		t.Fatalf("reject must succeed with SMTP down: %v", err)
	}
	if got, _ := r.documents.GetByID(ctx, d2.ID); got.Status != domain.DocumentRejected {
		t.Errorf("status = %s", got.Status)
	}
}

func TestEmailRetryBackoffAndTerminalFailureWipesPayload(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 1)
	r.enqueueMail(t, d.ID, 1)
	srv := smtptest.New(t, smtptest.Options{User: mailUser, Password: mailPass, FailStage: "END", FailReply: "550 5.1.1 mailbox unavailable"})

	clock := &atomicClock{}
	clock.Set(time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond))
	startEmailWorker(t, r.emailWorker(r.mailer(t, srv), 1, nil, clock))

	for attempt := 1; attempt <= email.MaxAttempts; attempt++ {
		waitFor(t, "attempt", func() bool {
			return r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE attempts = $1`, attempt) == 1
		})
		var status, lastErr string
		var next time.Time
		var hasPayload bool
		if err := r.db.Pool.QueryRow(ctx,
			`SELECT status, next_attempt_at, coalesce(last_error, ''), payload_ciphertext IS NOT NULL FROM email_outbox`).
			Scan(&status, &next, &lastErr, &hasPayload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(lastErr, "550") || strings.Contains(lastErr, "/sign/") || strings.Contains(lastErr, mailPass) {
			t.Errorf("attempt %d: last_error = %q", attempt, lastErr)
		}
		if attempt == email.MaxAttempts {
			if status != "failed" || hasPayload {
				t.Fatalf("final: status = %s, payload kept = %v", status, hasPayload)
			}
			break
		}
		want := clock.Now().Add(email.Backoff(attempt))
		if status != "pending" || !next.Equal(want) || !hasPayload {
			t.Fatalf("attempt %d: status=%s next=%v want=%v payload=%v", attempt, status, next, want, hasPayload)
		}
		time.Sleep(30 * time.Millisecond)
		if got := r.countWhere(t, `SELECT attempts FROM email_outbox`); got != attempt {
			t.Fatalf("retried before due: attempts = %d", got)
		}
		clock.Set(next)
	}
	clock.Add(48 * time.Hour)
	time.Sleep(50 * time.Millisecond)
	if got := r.countWhere(t, `SELECT attempts FROM email_outbox`); got != email.MaxAttempts {
		t.Errorf("attempts after failure = %d", got)
	}
	if len(srv.Messages()) != 0 {
		t.Error("server rejected every message")
	}
}

func TestTwoWorkerPoolsNeverDoubleSend(t *testing.T) {
	r := newRepos(t)
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 1)
	const rows = 24
	r.enqueueMail(t, d.ID, rows)
	srv := smtptest.New(t, smtptest.Options{User: mailUser, Password: mailPass})
	for i := 0; i < 2; i++ {
		startEmailWorker(t, r.emailWorker(r.mailer(t, srv), 3, nil, nil))
	}
	waitFor(t, "all sent", func() bool {
		return r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE status = 'sent'`) == rows
	})
	time.Sleep(100 * time.Millisecond)

	seen := map[string]int{}
	for _, m := range srv.Messages() {
		seen[m.To[0]]++
	}
	if len(seen) != rows {
		t.Fatalf("distinct recipients = %d, want %d", len(seen), rows)
	}
	for to, n := range seen {
		if n != 1 {
			t.Errorf("%s received %d copies", to, n)
		}
	}
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE attempts <> 1 OR payload_ciphertext IS NOT NULL`); n != 0 {
		t.Errorf("%d rows with attempts != 1 or payload retained", n)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestNoLinksOrSecretsInLogs(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	notifier := notify.NewLogNotifier(logger)

	good := smtptest.New(t, smtptest.Options{User: mailUser, Password: mailPass})
	flaky := smtptest.New(t, smtptest.Options{User: mailUser, Password: "other-password", FailStage: "END", FailReply: "451 later"})

	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 2)
	life := service.NewLifecycleService(r.documents, r.signers, r.audit, r.tx, service.SystemClock{}, notifier, "http://web.test").WithEmail(r.email, r.users)
	sign := service.NewSigningService(r.documents, r.signers, r.signatures, r.audit, nil, r.tx, service.SystemClock{}, service.UUIDGenerator{}, notifier, testSealSecret).
		WithEmail(r.email, r.users, "http://web.test")

	res, err := life.SendDocument(ctx, owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.TrimPrefix(res.Links[0].URL, "http://web.test/sign/")
	if _, err := sign.Approve(ctx, first, service.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	tokens := tokenSet{r: r, docID: d.ID, first: first}
	second := tokens.get(2)

	failClock := &atomicClock{}
	failClock.Set(time.Now().UTC().Add(time.Minute))
	failing := r.emailWorker(r.mailer(t, flaky), 1, logger, failClock)
	ctxF, cancelF := context.WithTimeout(ctx, 300*time.Millisecond)
	failing.Run(ctxF)
	cancelF()
	if n := r.countWhere(t, `SELECT count(*) FROM email_outbox WHERE attempts >= 1 AND status = 'pending'`); n == 0 {
		t.Fatal("failure path was not exercised")
	}

	startEmailWorker(t, r.emailWorker(r.mailer(t, good), 2, logger, failClock))
	failClock.Add(2 * time.Hour)
	waitFor(t, "delivery", func() bool { return len(good.Messages()) == 2 })

	out := logs.String()
	if out == "" {
		t.Fatal("no logs captured")
	}
	for name, needle := range map[string]string{
		"first token": first, "second token": second, "link prefix": "/sign/", "smtp password": mailPass, "other password": "other-password",
	} {
		if strings.Contains(out, needle) {
			t.Errorf("logs leak %s", name)
		}
	}
}
