package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

type lifeEnv struct {
	*env
	life     *service.LifecycleService
	signers  *fakes.Signers
	notifier *fakes.Notifier
}

func newLifeEnv() *lifeEnv {
	e := newEnv(1 << 20)
	signers := &fakes.Signers{Docs: e.docs}
	n := &fakes.Notifier{}
	return &lifeEnv{
		env:      e,
		life:     service.NewLifecycleService(e.docs, signers, e.audit, fakes.Tx{}, e.clock, n, "http://web.test/"),
		signers:  signers,
		notifier: n,
	}
}

func (e *lifeEnv) draft(t *testing.T) domain.Document {
	t.Helper()
	d, err := e.create("c.pdf", "Contract", pdfBytes(100), twoSigners)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (e *lifeEnv) stored(id string) domain.Document {
	d, _ := e.docs.GetByID(context.Background(), id)
	return d
}

func TestSendDocumentIssuesLinksAndPromotesFirstSigner(t *testing.T) {
	e := newLifeEnv()
	d := e.draft(t)

	res, err := e.life.SendDocument(context.Background(), owner, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Document.Status != domain.DocumentInProgress || len(res.Links) != 2 {
		t.Fatalf("unexpected result %+v", res)
	}

	got := e.stored(d.ID)
	pending := 0
	for i, s := range got.Signers {
		if s.Status == domain.SignerPending {
			pending++
		}
		link := res.Links[i]
		if link.SignerID != s.ID || link.Order != s.Order {
			t.Errorf("link %d mismatched signer: %+v vs %+v", i, link, s)
		}
		token := strings.TrimPrefix(link.URL, "http://web.test/sign/")
		if token == link.URL || len(token) < 40 {
			t.Errorf("bad url %q", link.URL)
		}
		if s.TokenHash != integrity.HashToken(token) || s.TokenHash == token {
			t.Errorf("signer %d: stored hash does not match sha256(token)", i)
		}
	}
	if pending != 1 || got.Signers[0].Status != domain.SignerPending {
		t.Errorf("want exactly signer #1 pending, got %+v", got.Signers)
	}
	if res.Links[0].URL == res.Links[1].URL {
		t.Error("tokens must be distinct")
	}

	last := e.audit.Events[len(e.audit.Events)-1]
	if last.Type != domain.EventDocumentSent || last.Actor != owner {
		t.Errorf("last event = %+v", last)
	}
	if len(e.notifier.Requests) != 1 || e.notifier.Requests[0].SignerID != got.Signers[0].ID {
		t.Errorf("notifier = %+v", e.notifier.Requests)
	}
}

func TestSendDocumentTwiceIsInvalidState(t *testing.T) {
	e := newLifeEnv()
	d := e.draft(t)
	if _, err := e.life.SendDocument(context.Background(), owner, d.ID); err != nil {
		t.Fatal(err)
	}
	events := len(e.audit.Events)
	_, err := e.life.SendDocument(context.Background(), owner, d.ID)
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("want ErrInvalidState, got %v", err)
	}
	if len(e.audit.Events) != events || len(e.notifier.Requests) != 1 {
		t.Error("second send must not emit events or notifications")
	}
}

func TestSendDocumentTerminalIsClosed(t *testing.T) {
	e := newLifeEnv()
	d := e.draft(t)
	if _, err := e.life.CancelDocument(context.Background(), owner, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.life.SendDocument(context.Background(), owner, d.ID); !errors.Is(err, domain.ErrDocumentClosed) {
		t.Fatalf("want ErrDocumentClosed, got %v", err)
	}
}

func TestSendAndCancelAreOwnerScoped(t *testing.T) {
	e := newLifeEnv()
	d := e.draft(t)
	other := "22222222-2222-4222-8222-222222222222"
	if _, err := e.life.SendDocument(context.Background(), other, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("send: want ErrNotFound, got %v", err)
	}
	if _, err := e.life.CancelDocument(context.Background(), other, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cancel: want ErrNotFound, got %v", err)
	}
	if e.stored(d.ID).Status != domain.DocumentDraft {
		t.Error("foreign owner changed document")
	}
}

func TestSendAuditFailureReportsErrorAndSkipsNotifier(t *testing.T) {
	e := newLifeEnv()
	d := e.draft(t)
	e.audit.Err = errors.New("boom")
	if _, err := e.life.SendDocument(context.Background(), owner, d.ID); err == nil {
		t.Fatal("expected error")
	}
	if len(e.notifier.Requests) != 0 {
		t.Error("notifier must only run after commit")
	}
}

func TestCancelFromEachState(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		prepare func(e *lifeEnv, id string)
		wantErr error
		prev    string
	}{
		{"draft", func(*lifeEnv, string) {}, nil, "draft"},
		{"in_progress", func(e *lifeEnv, id string) { _, _ = e.life.SendDocument(ctx, owner, id) }, nil, "in_progress"},
		{"completed", func(e *lifeEnv, id string) { setStatus(e, id, domain.DocumentCompleted) }, domain.ErrDocumentClosed, ""},
		{"rejected", func(e *lifeEnv, id string) { setStatus(e, id, domain.DocumentRejected) }, domain.ErrDocumentClosed, ""},
		{"cancelled", func(e *lifeEnv, id string) { _, _ = e.life.CancelDocument(ctx, owner, id) }, domain.ErrDocumentClosed, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newLifeEnv()
			d := e.draft(t)
			tc.prepare(e, d.ID)
			before := len(e.audit.Events)

			res, err := e.life.CancelDocument(ctx, owner, d.ID)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
				if len(e.audit.Events) != before {
					t.Error("failed cancel emitted an event")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != domain.DocumentCancelled {
				t.Errorf("status = %s", res.Status)
			}
			got := e.stored(d.ID)
			for _, s := range got.Signers {
				if s.TokenHash != "" || s.Status != domain.SignerWaiting {
					t.Errorf("signer not neutralised: %+v", s)
				}
			}
			ev := e.audit.Events[len(e.audit.Events)-1]
			if ev.Type != domain.EventDocumentCancelled || ev.Data["previous_status"] != tc.prev {
				t.Errorf("event = %+v", ev)
			}
		})
	}
}

func setStatus(e *lifeEnv, id string, st domain.DocumentStatus) {
	_ = e.docs.UpdateStatus(context.Background(), id, st, fixedNow, nil)
}

func TestListEventsOrderedAndOwnerScoped(t *testing.T) {
	e := newLifeEnv()
	d := e.draft(t)
	ctx := context.Background()
	if _, err := e.life.SendDocument(ctx, owner, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.life.CancelDocument(ctx, owner, d.ID); err != nil {
		t.Fatal(err)
	}
	events, err := e.life.ListEvents(ctx, owner, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	var types []domain.EventType
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	want := []domain.EventType{domain.EventDocumentCreated, domain.EventDocumentSent, domain.EventDocumentCancelled}
	if len(types) != 3 || types[0] != want[0] || types[1] != want[1] || types[2] != want[2] {
		t.Errorf("types = %v", types)
	}
	if _, err := e.life.ListEvents(ctx, "22222222-2222-4222-8222-222222222222", d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
