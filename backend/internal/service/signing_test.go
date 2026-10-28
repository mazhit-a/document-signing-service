package service_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

var sealSecret = []byte("0123456789abcdef0123456789abcdef")

type signEnv struct {
	*lifeEnv
	sign   *service.SigningService
	doc    domain.Document
	tokens []string
}

func newSignEnv(t *testing.T) *signEnv {
	t.Helper()
	le := newLifeEnv()
	e := &signEnv{lifeEnv: le}
	e.sign = service.NewSigningService(le.docs, le.signers, le.sigs, le.audit, le.files, fakes.Tx{}, le.clock, &fakes.SeqIDs{}, le.notifier, sealSecret)
	d := le.draft(t)
	res, err := le.life.SendDocument(context.Background(), owner, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.doc = d
	for _, l := range res.Links {
		e.tokens = append(e.tokens, strings.TrimPrefix(l.URL, "http://web.test/sign/"))
	}
	return e
}

var meta = service.ClientMeta{IP: "203.0.113.5", UserAgent: "test-agent"}

func eventTypes(e *signEnv) []domain.EventType {
	var out []domain.EventType
	for _, ev := range e.audit.Events {
		out = append(out, ev.Type)
	}
	return out
}

func TestApproveFirstSignerPromotesNext(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	res, err := e.sign.Approve(ctx, e.tokens[0], meta)
	if err != nil {
		t.Fatal(err)
	}
	if res.SignerStatus != domain.SignerSigned || res.DocumentStatus != domain.DocumentInProgress || res.Seal == "" {
		t.Fatalf("unexpected result %+v", res)
	}
	if res.SignedAt.Nanosecond()%1000 != 0 {
		t.Errorf("signed_at not microsecond-truncated: %v", res.SignedAt)
	}
	got := e.stored(e.doc.ID)
	if got.Signers[0].Status != domain.SignerSigned || got.Signers[1].Status != domain.SignerPending {
		t.Fatalf("signers = %+v", got.Signers)
	}
	if err := got.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	if len(e.sigs.Items) != 1 {
		t.Fatalf("want 1 signature, got %d", len(e.sigs.Items))
	}
	sig := e.sigs.Items[0]
	wantPayload := integrity.BuildPayload(e.doc.ID, got.SHA256, got.Signers[0].ID, 1, res.SignedAt)
	if sig.Payload != wantPayload || sig.Seal != res.Seal || !integrity.VerifySeal(sealSecret, sig.Payload, sig.Seal) {
		t.Errorf("bad seal: %+v", sig)
	}
	if sig.IP != meta.IP || sig.UserAgent != meta.UserAgent || sig.DocumentSHA256 != got.SHA256 {
		t.Errorf("bad signature meta: %+v", sig)
	}
	types := eventTypes(e)
	if types[len(types)-1] != domain.EventSignerApproved {
		t.Errorf("events = %v", types)
	}
	if n := len(e.notifier.Requests); n != 2 || e.notifier.Requests[1].SignerID != got.Signers[1].ID {
		t.Errorf("notifier = %+v", e.notifier.Requests)
	}
}

func TestApproveIsIdempotent(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	first, err := e.sign.Approve(ctx, e.tokens[0], meta)
	if err != nil {
		t.Fatal(err)
	}
	events, notes := len(e.audit.Events), len(e.notifier.Requests)
	second, err := e.sign.Approve(ctx, e.tokens[0], service.ClientMeta{IP: "198.51.100.1", UserAgent: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("responses differ: %+v vs %+v", first, second)
	}
	if len(e.sigs.Items) != 1 || len(e.audit.Events) != events || len(e.notifier.Requests) != notes {
		t.Error("repeat approve must not add signatures, events or notifications")
	}
}

func TestApproveLastSignerCompletesDocument(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	if _, err := e.sign.Approve(ctx, e.tokens[0], meta); err != nil {
		t.Fatal(err)
	}
	e.clock.T = e.clock.T.Add(time.Hour)
	res, err := e.sign.Approve(ctx, e.tokens[1], meta)
	if err != nil {
		t.Fatal(err)
	}
	if res.DocumentStatus != domain.DocumentCompleted {
		t.Fatalf("result %+v", res)
	}
	got := e.stored(e.doc.ID)
	if got.Status != domain.DocumentCompleted || got.CompletedAt == nil || !got.CompletedAt.Equal(res.SignedAt) {
		t.Fatalf("document = %+v", got)
	}
	if err := got.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	types := eventTypes(e)
	n := len(types)
	if types[n-2] != domain.EventSignerApproved || types[n-1] != domain.EventDocumentCompleted {
		t.Errorf("events = %v", types)
	}
	again, err := e.sign.Approve(ctx, e.tokens[1], meta)
	if err != nil || again != res {
		t.Errorf("repeat after completion: %+v %v", again, err)
	}
}

func TestApproveOutOfTurnIsNotYourTurn(t *testing.T) {
	e := newSignEnv(t)
	_, err := e.sign.Approve(context.Background(), e.tokens[1], meta)
	if !errors.Is(err, domain.ErrNotYourTurn) {
		t.Fatalf("want ErrNotYourTurn, got %v", err)
	}
	if len(e.sigs.Items) != 0 {
		t.Error("no signature may be written")
	}
}

func TestUnknownAndMalformedTokensAreNotFound(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	for _, tok := range []string{"", "nope", strings.Repeat("A", 43), "../etc/passwd", e.tokens[0] + "x"} {
		if _, err := e.sign.Approve(ctx, tok, meta); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("approve %q: %v", tok, err)
		}
		if _, err := e.sign.Reject(ctx, tok, "r"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("reject %q: %v", tok, err)
		}
		if _, err := e.sign.View(ctx, tok); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("view %q: %v", tok, err)
		}
	}
}

func TestRejectClosesDocumentForEveryone(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	res, err := e.sign.Reject(ctx, e.tokens[0], "  Wrong amount ")
	if err != nil {
		t.Fatal(err)
	}
	if res.SignerStatus != domain.SignerRejected || res.DocumentStatus != domain.DocumentRejected {
		t.Fatalf("result %+v", res)
	}
	got := e.stored(e.doc.ID)
	if got.Status != domain.DocumentRejected || got.Signers[0].RejectReason != "Wrong amount" || got.Signers[0].DecidedAt == nil {
		t.Fatalf("document = %+v", got)
	}
	types := eventTypes(e)
	n := len(types)
	if types[n-2] != domain.EventSignerRejected || types[n-1] != domain.EventDocumentRejected {
		t.Errorf("events = %v", types)
	}
	if _, err := e.sign.Approve(ctx, e.tokens[0], meta); !errors.Is(err, domain.ErrDocumentClosed) {
		t.Errorf("rejector approve: %v", err)
	}
	if _, err := e.sign.Reject(ctx, e.tokens[0], "again"); !errors.Is(err, domain.ErrDocumentClosed) {
		t.Errorf("repeat reject: %v", err)
	}
	if _, err := e.sign.Approve(ctx, e.tokens[1], meta); !errors.Is(err, domain.ErrDocumentClosed) {
		t.Errorf("later signer approve: %v", err)
	}
	if _, err := e.sign.Reject(ctx, e.tokens[1], "me too"); !errors.Is(err, domain.ErrDocumentClosed) {
		t.Errorf("later signer reject: %v", err)
	}
	if len(e.sigs.Items) != 0 {
		t.Error("rejected document must hold no signatures")
	}
}

func TestRejectValidatesReason(t *testing.T) {
	e := newSignEnv(t)
	for _, reason := range []string{"", "   ", strings.Repeat("я", 501)} {
		_, err := e.sign.Reject(context.Background(), e.tokens[0], reason)
		var ve *domain.ValidationError
		if !errors.As(err, &ve) || ve.Fields["reason"] == "" {
			t.Errorf("reason %q: %v", reason, err)
		}
	}
	if _, err := e.sign.Reject(context.Background(), e.tokens[0], strings.Repeat("я", 500)); err != nil {
		t.Errorf("500 runes must pass: %v", err)
	}
}

func TestRejectOutOfTurnAndAfterSigning(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	if _, err := e.sign.Reject(ctx, e.tokens[1], "early"); !errors.Is(err, domain.ErrNotYourTurn) {
		t.Errorf("waiting reject: %v", err)
	}
	if _, err := e.sign.Approve(ctx, e.tokens[0], meta); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sign.Reject(ctx, e.tokens[0], "late"); !errors.Is(err, domain.ErrInvalidState) {
		t.Errorf("signed reject: %v", err)
	}
	if _, err := e.sign.Approve(ctx, e.tokens[1], meta); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sign.Reject(ctx, e.tokens[0], "late"); !errors.Is(err, domain.ErrDocumentClosed) {
		t.Errorf("signed reject after completion: %v", err)
	}
}

func TestCancelledDocumentTokensAreNotFound(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	if _, err := e.life.CancelDocument(ctx, owner, e.doc.ID); err != nil {
		t.Fatal(err)
	}
	for i, tok := range e.tokens {
		if _, err := e.sign.Approve(ctx, tok, meta); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("token %d approve: %v", i, err)
		}
	}
}

func TestViewCanAct(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	v0, err := e.sign.View(ctx, e.tokens[0])
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := e.sign.View(ctx, e.tokens[1])
	if !v0.CanAct || v1.CanAct || v0.Total != 2 || v0.Signer.Order != 1 || v1.Signer.Status != domain.SignerWaiting {
		t.Fatalf("v0=%+v v1=%+v", v0, v1)
	}
	if _, err := e.sign.Approve(ctx, e.tokens[0], meta); err != nil {
		t.Fatal(err)
	}
	v0, _ = e.sign.View(ctx, e.tokens[0])
	v1, _ = e.sign.View(ctx, e.tokens[1])
	if v0.CanAct || !v1.CanAct {
		t.Errorf("after approve: v0=%v v1=%v", v0.CanAct, v1.CanAct)
	}
	if _, err := e.sign.Approve(ctx, e.tokens[1], meta); err != nil {
		t.Fatal(err)
	}
	v1, _ = e.sign.View(ctx, e.tokens[1])
	if v1.CanAct || v1.Document.Status != domain.DocumentCompleted {
		t.Errorf("after completion: %+v", v1)
	}
}

func TestViewRejectedDocumentHasNoActors(t *testing.T) {
	e := newSignEnv(t)
	ctx := context.Background()
	if _, err := e.sign.Reject(ctx, e.tokens[0], "no"); err != nil {
		t.Fatal(err)
	}
	for i, tok := range e.tokens {
		v, err := e.sign.View(ctx, tok)
		if err != nil || v.CanAct {
			t.Errorf("token %d: %+v %v", i, v, err)
		}
	}
}

func TestOpenFileByToken(t *testing.T) {
	e := newSignEnv(t)
	doc, rc, err := e.sign.OpenFile(context.Background(), e.tokens[1])
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if int64(len(b)) != doc.SizeBytes {
		t.Errorf("read %d bytes, want %d", len(b), doc.SizeBytes)
	}
	if _, _, err := e.sign.OpenFile(context.Background(), "bogus"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("bogus token: %v", err)
	}
}

func TestApproveAuditFailureLeavesNoPartialStateInFakeAndSkipsNotifier(t *testing.T) {
	e := newSignEnv(t)
	e.audit.Err = errors.New("boom")
	notes := len(e.notifier.Requests)
	if _, err := e.sign.Approve(context.Background(), e.tokens[0], meta); err == nil {
		t.Fatal("expected error")
	}
	if len(e.notifier.Requests) != notes {
		t.Error("notifier must only run after commit")
	}
}
