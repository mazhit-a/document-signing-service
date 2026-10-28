package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

func (r *repos) assertChainValid(t *testing.T, docID string) []domain.AuditEvent {
	t.Helper()
	events, err := r.audit.ListByDocument(context.Background(), docID)
	if err != nil {
		t.Fatal(err)
	}
	chain := make([]integrity.Event, len(events))
	for i, e := range events {
		chain[i] = integrity.Event{ID: e.ID, Type: string(e.Type), Actor: e.Actor, Data: e.Data, CreatedAt: e.CreatedAt, PrevHash: e.PrevHash, Hash: e.Hash}
	}
	if rep := integrity.VerifyChain(chain); !rep.OK {
		t.Fatalf("audit chain broken: %+v", rep)
	}
	return events
}

func (r *repos) assertConsistent(t *testing.T, docID string) domain.Document {
	t.Helper()
	got, err := r.documents.GetByID(context.Background(), docID)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.CheckInvariants(); err != nil {
		t.Fatalf("invariants: %v", err)
	}
	return got
}

func countEvents(events []domain.AuditEvent, typ domain.EventType) int {
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func TestParallelApprovesBySignersInOrder(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	_, d, tokens := r.sent(t, 4)
	svc := r.signing(r.audit)

	deadline := time.Now().Add(20 * time.Second)
	errs := make([]error, len(tokens))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := len(tokens) - 1; i >= 0; i-- {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				_, err := svc.Approve(ctx, tokens[i], service.ClientMeta{})
				if !errors.Is(err, domain.ErrNotYourTurn) || time.Now().After(deadline) {
					errs[i] = err
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("signer %d: %v", i+1, err)
		}
	}
	got := r.assertConsistent(t, d.ID)
	if got.Status != domain.DocumentCompleted || got.CompletedAt == nil {
		t.Fatalf("document = %+v", got)
	}
	sigs, err := r.signatures.ListByDocument(ctx, d.ID)
	if err != nil || len(sigs) != len(tokens) {
		t.Fatalf("signatures = %d, %v", len(sigs), err)
	}
	for i, s := range sigs {
		if s.SignerID != got.Signers[i].ID {
			t.Errorf("signature %d belongs to signer %s, want %s", i, s.SignerID, got.Signers[i].ID)
		}
		if i > 0 && s.SignedAt.Before(sigs[i-1].SignedAt) {
			t.Errorf("signature %d signed before signature %d", i, i-1)
		}
		want := integrity.BuildPayload(d.ID, d.SHA256, got.Signers[i].ID, i+1, s.SignedAt)
		if s.Payload != want || !integrity.VerifySeal(testSealSecret, s.Payload, s.Seal) {
			t.Errorf("signature %d payload/seal invalid", i)
		}
	}
	events := r.assertChainValid(t, d.ID)
	if countEvents(events, domain.EventSignerApproved) != len(tokens) || countEvents(events, domain.EventDocumentCompleted) != 1 {
		t.Errorf("events = %+v", events)
	}
}

func TestConcurrentSendProducesOneSend(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 3)
	svc := r.lifecycle(r.audit)

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = svc.SendDocument(ctx, owner.ID, d.ID)
		}()
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, domain.ErrInvalidState):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("successful sends = %d, want 1", ok)
	}
	got := r.assertConsistent(t, d.ID)
	if got.Status != domain.DocumentInProgress {
		t.Errorf("status = %s", got.Status)
	}
	if c := r.countWhere(t, `SELECT count(*) FROM signers WHERE document_id = $1::uuid AND token_hash IS NOT NULL`, d.ID); c != 3 {
		t.Errorf("token hashes = %d, want 3", c)
	}
	events := r.assertChainValid(t, d.ID)
	if countEvents(events, domain.EventDocumentSent) != 1 {
		t.Errorf("events = %+v", events)
	}
}

func TestConcurrentSendAndCancelEndCancelled(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 2)
	life := r.lifecycle(r.audit)

	var wg sync.WaitGroup
	var sendErr, cancelErr error
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, sendErr = life.SendDocument(ctx, owner.ID, d.ID)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, cancelErr = life.CancelDocument(ctx, owner.ID, d.ID)
	}()
	close(start)
	wg.Wait()

	if cancelErr != nil {
		t.Fatalf("cancel must always win eventually: %v", cancelErr)
	}
	if sendErr != nil && !errors.Is(sendErr, domain.ErrDocumentClosed) {
		t.Fatalf("send: %v", sendErr)
	}
	got := r.assertConsistent(t, d.ID)
	if got.Status != domain.DocumentCancelled {
		t.Errorf("status = %s", got.Status)
	}
	if c := r.countWhere(t, `SELECT count(*) FROM signers WHERE document_id = $1::uuid AND token_hash IS NOT NULL`, d.ID); c != 0 {
		t.Errorf("live token hashes after cancel = %d", c)
	}
	events := r.assertChainValid(t, d.ID)
	if countEvents(events, domain.EventDocumentCancelled) != 1 {
		t.Errorf("events = %+v", events)
	}
	if (sendErr == nil) != (countEvents(events, domain.EventDocumentSent) == 1) {
		t.Errorf("sendErr=%v but events = %+v", sendErr, events)
	}
}

func TestConcurrentCancelAndApproveStayConsistent(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner, d, tokens := r.sent(t, 2)
	life := r.lifecycle(r.audit)
	svc := r.signing(r.audit)

	var wg sync.WaitGroup
	var approveErr, cancelErr error
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
		_, cancelErr = life.CancelDocument(ctx, owner.ID, d.ID)
	}()
	close(start)
	wg.Wait()

	if cancelErr != nil {
		t.Fatalf("cancel: %v", cancelErr)
	}
	if approveErr != nil && !errors.Is(approveErr, domain.ErrNotFound) {
		t.Fatalf("approve: %v", approveErr)
	}
	got := r.assertConsistent(t, d.ID)
	if got.Status != domain.DocumentCancelled {
		t.Errorf("status = %s", got.Status)
	}
	sigs := r.countWhere(t, `SELECT count(*) FROM signatures WHERE document_id = $1::uuid`, d.ID)
	if (approveErr == nil) != (sigs == 1) {
		t.Errorf("signatures = %d with approveErr=%v", sigs, approveErr)
	}
	events := r.assertChainValid(t, d.ID)
	if countEvents(events, domain.EventSignerApproved) != sigs || countEvents(events, domain.EventDocumentCancelled) != 1 {
		t.Errorf("events = %+v", events)
	}
	if last := events[len(events)-1]; last.Type != domain.EventDocumentCancelled {
		t.Errorf("last event = %s", last.Type)
	}
	if _, err := svc.Approve(ctx, tokens[1], service.ClientMeta{}); err == nil {
		t.Error("second signer must not be able to sign a cancelled document")
	}
}
