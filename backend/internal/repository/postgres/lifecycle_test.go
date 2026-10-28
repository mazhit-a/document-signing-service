package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

type failingAudit struct {
	service.AuditRepository
}

func (failingAudit) Append(context.Context, string, domain.EventType, string, map[string]any, time.Time) (domain.AuditEvent, error) {
	return domain.AuditEvent{}, errors.New("audit down")
}

func (r *repos) lifecycle(audit service.AuditRepository) *service.LifecycleService {
	return service.NewLifecycleService(r.documents, r.signers, audit, r.tx, service.SystemClock{}, notify.Noop{}, "http://web.test")
}

func (r *repos) countWhere(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := r.db.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSendStoresOnlyHashesAndOnePendingSigner(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 3)
	svc := r.lifecycle(r.audit)

	res, err := svc.SendDocument(ctx, owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendDocument(ctx, owner.ID, d.ID); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("second send: want ErrInvalidState, got %v", err)
	}

	got, err := r.documents.GetForOwner(ctx, owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DocumentInProgress {
		t.Fatalf("status = %s", got.Status)
	}
	if err := got.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	for i, s := range got.Signers {
		token := strings.TrimPrefix(res.Links[i].URL, "http://web.test/sign/")
		if s.TokenHash != integrity.HashToken(token) {
			t.Errorf("signer %d hash mismatch", i)
		}
		if n := r.countWhere(t, `SELECT count(*) FROM signers WHERE token_hash = $1`, token); n != 0 {
			t.Errorf("raw token stored for signer %d", i)
		}
	}
	if got.Signers[0].Status != domain.SignerPending || got.Signers[1].Status != domain.SignerWaiting || got.Signers[2].Status != domain.SignerWaiting {
		t.Errorf("signer statuses: %+v", got.Signers)
	}

	events, err := svc.ListEvents(ctx, owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != domain.EventDocumentSent {
		t.Fatalf("events = %+v", events)
	}
	var chain []integrity.Event
	for _, e := range events {
		chain = append(chain, integrity.Event{ID: e.ID, Type: string(e.Type), Actor: e.Actor, Data: e.Data, CreatedAt: e.CreatedAt, PrevHash: e.PrevHash, Hash: e.Hash})
	}
	if rep := integrity.VerifyChain(chain); !rep.OK {
		t.Errorf("chain broken: %+v", rep)
	}
}

func TestSendRollsBackWhenAuditFails(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 2)

	if _, err := r.lifecycle(failingAudit{r.audit}).SendDocument(ctx, owner.ID, d.ID); err == nil {
		t.Fatal("expected error")
	}

	got, err := r.documents.GetForOwner(ctx, owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DocumentDraft {
		t.Errorf("status = %s, want draft", got.Status)
	}
	for _, s := range got.Signers {
		if s.TokenHash != "" || s.Status != domain.SignerWaiting {
			t.Errorf("signer leaked partial state: %+v", s)
		}
	}
	if n := r.countWhere(t, `SELECT count(*) FROM audit_events WHERE document_id = $1::uuid`, d.ID); n != 0 {
		t.Errorf("%d audit events survived rollback", n)
	}
}

func TestCancelRollsBackWhenAuditFails(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 2)
	if _, err := r.lifecycle(r.audit).SendDocument(ctx, owner.ID, d.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := r.lifecycle(failingAudit{r.audit}).CancelDocument(ctx, owner.ID, d.ID); err == nil {
		t.Fatal("expected error")
	}
	got, _ := r.documents.GetForOwner(ctx, owner.ID, d.ID)
	if got.Status != domain.DocumentInProgress || got.Signers[0].Status != domain.SignerPending || got.Signers[0].TokenHash == "" {
		t.Errorf("cancel was not rolled back: %+v", got)
	}
	if n := r.countWhere(t, `SELECT count(*) FROM audit_events WHERE document_id = $1::uuid`, d.ID); n != 1 {
		t.Errorf("want 1 audit event, got %d", n)
	}
}

func TestCancelInvalidatesTokensAndIsTerminal(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	svc := r.lifecycle(r.audit)

	var hashes []string
	for i := 0; i < 2; i++ {
		d := r.doc(t, owner.ID, baseTime.Add(0), domain.DocumentDraft, 2)
		res, err := svc.SendDocument(ctx, owner.ID, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range res.Links {
			hashes = append(hashes, integrity.HashToken(strings.TrimPrefix(l.URL, "http://web.test/sign/")))
		}
		if _, err := svc.CancelDocument(ctx, owner.ID, d.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CancelDocument(ctx, owner.ID, d.ID); !errors.Is(err, domain.ErrDocumentClosed) {
			t.Fatalf("second cancel: %v", err)
		}
		got, _ := r.documents.GetForOwner(ctx, owner.ID, d.ID)
		if got.Status != domain.DocumentCancelled {
			t.Fatalf("status = %s", got.Status)
		}
		if err := got.CheckInvariants(); err != nil {
			t.Fatal(err)
		}
		for _, s := range got.Signers {
			if s.TokenHash != "" {
				t.Errorf("token hash survived cancel")
			}
		}
	}
	for _, h := range hashes {
		if _, err := r.signers.GetByTokenHash(ctx, h); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("cancelled token still resolves: %v", err)
		}
	}
}

func TestListEventsOwnerScoped(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	other := r.user(t, "x@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 1)
	svc := r.lifecycle(r.audit)
	if _, err := svc.SendDocument(ctx, owner.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CancelDocument(ctx, owner.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	events, err := svc.ListEvents(ctx, owner.ID, d.ID)
	if err != nil || len(events) != 2 || events[0].ID >= events[1].ID {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if _, err := svc.ListEvents(ctx, other.ID, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := svc.SendDocument(ctx, other.ID, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("foreign send: want ErrNotFound, got %v", err)
	}
}
