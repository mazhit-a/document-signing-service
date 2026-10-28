package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

var testSealSecret = []byte("0123456789abcdef0123456789abcdef")

func (r *repos) signing(audit service.AuditRepository) *service.SigningService {
	return service.NewSigningService(r.documents, r.signers, r.signatures, audit, fakes.NewStorage(), r.tx, service.SystemClock{}, service.UUIDGenerator{}, notify.Noop{}, testSealSecret)
}

func (r *repos) sent(t *testing.T, signers int) (domain.User, domain.Document, []string) {
	t.Helper()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, signers)
	res, err := r.lifecycle(r.audit).SendDocument(context.Background(), owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	for _, l := range res.Links {
		tokens = append(tokens, strings.TrimPrefix(l.URL, "http://web.test/sign/"))
	}
	return owner, d, tokens
}

func TestSigningFullFlowKeepsChainAndInvariants(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner, d, tokens := r.sent(t, 3)
	svc := r.signing(r.audit)
	meta := service.ClientMeta{IP: "203.0.113.9", UserAgent: "pg-test"}

	var last service.DecisionResult
	for i, tok := range tokens {
		res, err := svc.Approve(ctx, tok, meta)
		if err != nil {
			t.Fatalf("approve %d: %v", i, err)
		}
		last = res
		got, _ := r.documents.GetForOwner(ctx, owner.ID, d.ID)
		if err := got.CheckInvariants(); err != nil {
			t.Fatalf("after approve %d: %v", i, err)
		}
	}
	if last.DocumentStatus != domain.DocumentCompleted {
		t.Fatalf("last = %+v", last)
	}
	got, _ := r.documents.GetForOwner(ctx, owner.ID, d.ID)
	if got.Status != domain.DocumentCompleted || got.CompletedAt == nil || !got.CompletedAt.Equal(last.SignedAt) {
		t.Fatalf("document = %+v", got)
	}

	sigs, err := r.signatures.ListByDocument(ctx, d.ID)
	if err != nil || len(sigs) != 3 {
		t.Fatalf("signatures = %d, %v", len(sigs), err)
	}
	for i, s := range sigs {
		want := integrity.BuildPayload(d.ID, d.SHA256, got.Signers[i].ID, i+1, s.SignedAt)
		if s.Payload != want || !integrity.VerifySeal(testSealSecret, s.Payload, s.Seal) {
			t.Errorf("signature %d payload/seal invalid after DB round trip", i)
		}
		if s.IP != "203.0.113.9" || s.UserAgent != "pg-test" {
			t.Errorf("signature %d meta = %q %q", i, s.IP, s.UserAgent)
		}
	}

	events, err := r.audit.ListByDocument(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	var types []domain.EventType
	var chain []integrity.Event
	for _, e := range events {
		types = append(types, e.Type)
		chain = append(chain, integrity.Event{ID: e.ID, Type: string(e.Type), Actor: e.Actor, Data: e.Data, CreatedAt: e.CreatedAt, PrevHash: e.PrevHash, Hash: e.Hash})
	}
	want := []domain.EventType{
		domain.EventDocumentSent,
		domain.EventSignerApproved, domain.EventSignerApproved, domain.EventSignerApproved,
		domain.EventDocumentCompleted,
	}
	if len(types) != len(want) {
		t.Fatalf("events = %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("events = %v", types)
		}
	}
	if rep := integrity.VerifyChain(chain); !rep.OK {
		t.Errorf("chain broken: %+v", rep)
	}
}

func TestSigningRejectFlow(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner, d, tokens := r.sent(t, 2)
	svc := r.signing(r.audit)

	if _, err := svc.Reject(ctx, tokens[0], "Wrong salary amount"); err != nil {
		t.Fatal(err)
	}
	got, _ := r.documents.GetForOwner(ctx, owner.ID, d.ID)
	if got.Status != domain.DocumentRejected || got.Signers[0].Status != domain.SignerRejected || got.Signers[0].RejectReason != "Wrong salary amount" {
		t.Fatalf("document = %+v", got)
	}
	if err := got.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, tokens[1], service.ClientMeta{}); !errors.Is(err, domain.ErrDocumentClosed) {
		t.Errorf("later signer: %v", err)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM signatures WHERE document_id = $1::uuid`, d.ID); n != 0 {
		t.Errorf("signatures = %d", n)
	}
	events, _ := r.audit.ListByDocument(ctx, d.ID)
	if n := len(events); n != 3 || events[1].Type != domain.EventSignerRejected || events[2].Type != domain.EventDocumentRejected {
		t.Errorf("events = %+v", events)
	}
}

func TestSigningRollsBackWhenAuditFails(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner, d, tokens := r.sent(t, 2)

	if _, err := r.signing(failingAudit{r.audit}).Approve(ctx, tokens[0], service.ClientMeta{}); err == nil {
		t.Fatal("expected error")
	}
	got, _ := r.documents.GetForOwner(ctx, owner.ID, d.ID)
	if got.Signers[0].Status != domain.SignerPending || got.Signers[1].Status != domain.SignerWaiting {
		t.Errorf("partial state: %+v", got.Signers)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM signatures WHERE document_id = $1::uuid`, d.ID); n != 0 {
		t.Errorf("signature survived rollback: %d", n)
	}
	if _, err := r.signing(r.audit).Approve(ctx, tokens[0], service.ClientMeta{}); err != nil {
		t.Errorf("retry after rollback: %v", err)
	}
}

func TestConcurrentApprovesProduceExactlyOneSignature(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	_, d, tokens := r.sent(t, 2)
	svc := r.signing(r.audit)

	const n = 12
	results := make([]service.DecisionResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = svc.Approve(ctx, tokens[0], service.ClientMeta{IP: "203.0.113.1"})
		}()
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("approve %d: %v", i, errs[i])
		}
		if results[i] != results[0] {
			t.Errorf("result %d differs: %+v vs %+v", i, results[i], results[0])
		}
	}
	if c := r.countWhere(t, `SELECT count(*) FROM signatures WHERE document_id = $1::uuid`, d.ID); c != 1 {
		t.Errorf("signatures = %d, want 1", c)
	}
	if c := r.countWhere(t, `SELECT count(*) FROM audit_events WHERE document_id = $1::uuid AND type = 'signer.approved'`, d.ID); c != 1 {
		t.Errorf("approved events = %d, want 1", c)
	}
	got, _ := r.documents.GetByID(ctx, d.ID)
	if err := got.CheckInvariants(); err != nil {
		t.Error(err)
	}
}

func TestConcurrentApproveAndRejectLeaveOneOutcome(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	_, d, tokens := r.sent(t, 2)
	svc := r.signing(r.audit)

	var wg sync.WaitGroup
	var approveErr, rejectErr error
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, approveErr = svc.Approve(ctx, tokens[0], service.ClientMeta{})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, rejectErr = svc.Reject(ctx, tokens[0], "no")
	}()
	close(start)
	wg.Wait()

	if (approveErr == nil) == (rejectErr == nil) {
		t.Fatalf("exactly one must win: approve=%v reject=%v", approveErr, rejectErr)
	}
	got, _ := r.documents.GetByID(ctx, d.ID)
	if err := got.CheckInvariants(); err != nil {
		t.Error(err)
	}
	sigs := r.countWhere(t, `SELECT count(*) FROM signatures WHERE document_id = $1::uuid`, d.ID)
	if (approveErr == nil) != (sigs == 1) {
		t.Errorf("signatures = %d with approveErr=%v", sigs, approveErr)
	}
}
