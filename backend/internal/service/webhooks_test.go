package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

func TestApproveEnqueuesWebhookEvents(t *testing.T) {
	e := newSignEnv(t)
	outbox := &fakes.Outbox{}
	e.sign.WithOutbox(outbox)
	ctx := context.Background()

	if _, err := e.sign.Approve(ctx, e.tokens[0], meta); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sign.Approve(ctx, e.tok(2), meta); err != nil {
		t.Fatal(err)
	}
	var got []domain.EventType
	for _, ev := range outbox.Events {
		got = append(got, ev.Type)
	}
	want := []domain.EventType{domain.EventSignerApproved, domain.EventSignerApproved, domain.EventDocumentCompleted}
	if len(got) != len(want) {
		t.Fatalf("enqueued = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("enqueued = %v, want %v", got, want)
		}
	}
}

func TestRepeatedApproveDoesNotEnqueueAgain(t *testing.T) {
	e := newSignEnv(t)
	outbox := &fakes.Outbox{}
	e.sign.WithOutbox(outbox)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := e.sign.Approve(ctx, e.tokens[0], meta); err != nil {
			t.Fatal(err)
		}
	}
	if len(outbox.Events) != 1 {
		t.Fatalf("enqueued %d events, want 1", len(outbox.Events))
	}
}

func TestRejectEnqueuesOnlyDocumentRejected(t *testing.T) {
	e := newSignEnv(t)
	outbox := &fakes.Outbox{}
	e.sign.WithOutbox(outbox)
	if _, err := e.sign.Reject(context.Background(), e.tokens[0], "nope"); err != nil {
		t.Fatal(err)
	}
	if len(outbox.Events) != 1 || outbox.Events[0].Type != domain.EventDocumentRejected {
		t.Fatalf("enqueued = %+v", outbox.Events)
	}
}

func TestOutboxFailureFailsTheOperation(t *testing.T) {
	e := newSignEnv(t)
	boom := errors.New("outbox down")
	e.sign.WithOutbox(&fakes.Outbox{Err: boom})
	if _, err := e.sign.Approve(context.Background(), e.tokens[0], meta); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestWebhookServiceCreate(t *testing.T) {
	clock := &fakes.Clock{}
	svc := service.NewWebhookService(&fakes.WebhookEndpoints{}, clock, &fakes.SeqIDs{})
	ctx := context.Background()

	a, err := svc.Create(ctx, owner, "https://example.com/hook")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(ctx, owner, "http://localhost:9000/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.Secret, "whsec_") || len(a.Secret) < 40 || a.Secret == b.Secret || !a.Active || a.OwnerID != owner {
		t.Fatalf("endpoint = %+v", a)
	}
}

func TestWebhookServiceRejectsBadURLs(t *testing.T) {
	svc := service.NewWebhookService(&fakes.WebhookEndpoints{}, &fakes.Clock{}, &fakes.SeqIDs{})
	for _, u := range []string{"", "not a url", "ftp://example.com", "/relative", "http://", "http://user:pw@example.com/", "https://example.com/" + strings.Repeat("a", 2100)} {
		_, err := svc.Create(context.Background(), owner, u)
		var ve *domain.ValidationError
		if !errors.As(err, &ve) || ve.Fields["url"] == "" {
			t.Errorf("url %q: err = %v", u, err)
		}
	}
}

func TestWebhookServiceListAndDeleteAreOwnerScoped(t *testing.T) {
	svc := service.NewWebhookService(&fakes.WebhookEndpoints{}, &fakes.Clock{}, &fakes.SeqIDs{})
	ctx := context.Background()
	e, _ := svc.Create(ctx, owner, "https://example.com/hook")

	if list, _ := svc.List(ctx, "someone-else"); len(list) != 0 {
		t.Fatalf("list = %+v", list)
	}
	if err := svc.Delete(ctx, "someone-else", e.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := svc.Delete(ctx, owner, e.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.List(ctx, owner); len(list) != 0 {
		t.Fatalf("list = %+v", list)
	}
}
