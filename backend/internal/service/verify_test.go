package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

func newVerifier(e *signEnv) *service.VerifyService {
	return service.NewVerifyService(e.docs, e.sigs, e.audit, e.files, fakes.Tx{}, e.clock, sealSecret)
}

func completed(t *testing.T) *signEnv {
	t.Helper()
	e := newSignEnv(t)
	for order := 1; order <= 2; order++ {
		tok := e.tokens[0]
		if order > 1 {
			tok = e.tok(order)
		}
		if _, err := e.sign.Approve(context.Background(), tok, meta); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func TestVerifyUntouchedCompletedDocumentIsValid(t *testing.T) {
	e := completed(t)
	rep, err := newVerifier(e).Verify(context.Background(), owner, e.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Valid || !rep.File.OK || !rep.AuditChain.OK || len(rep.Signatures) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.File.ExpectedSHA256 != rep.File.ActualSHA256 {
		t.Errorf("hashes differ: %+v", rep.File)
	}
	for _, s := range rep.Signatures {
		if !s.SealOK || !s.HashMatchesDocument || s.Order == 0 {
			t.Errorf("signature = %+v", s)
		}
	}
	if rep.AuditChain.Events != 5 || rep.AuditChain.FirstBrokenEventID != nil {
		t.Errorf("chain = %+v", rep.AuditChain)
	}
}

func TestVerifyAppendsEventAndChainStaysValid(t *testing.T) {
	e := completed(t)
	v := newVerifier(e)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		rep, err := v.Verify(ctx, owner, e.doc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Valid || rep.AuditChain.Events != 5+i-1 {
			t.Fatalf("call %d: %+v", i, rep)
		}
	}
	types := eventTypes(e)
	if n := len(types); n != 8 || types[n-1] != domain.EventDocumentVerified {
		t.Fatalf("events = %v", types)
	}
	if got := e.audit.Events[7].Data["result"]; got != "valid" {
		t.Errorf("result = %v", got)
	}
}

func TestVerifyDetectsTamperedFile(t *testing.T) {
	e := completed(t)
	e.files.Files[e.stored(e.doc.ID).StorageKey] = []byte("tampered")
	rep, err := newVerifier(e).Verify(context.Background(), owner, e.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid || rep.File.OK || rep.File.ActualSHA256 == "" || !rep.AuditChain.OK {
		t.Fatalf("report = %+v", rep)
	}
	if e.audit.Events[len(e.audit.Events)-1].Data["result"] != "invalid" {
		t.Error("verify event should record invalid")
	}
}

func TestVerifyMissingFileIsFailedCheckNotError(t *testing.T) {
	e := completed(t)
	delete(e.files.Files, e.stored(e.doc.ID).StorageKey)
	rep, err := newVerifier(e).Verify(context.Background(), owner, e.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid || rep.File.OK || rep.File.ActualSHA256 != "" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyDetectsTamperedSignaturePayload(t *testing.T) {
	e := completed(t)
	e.sigs.Items[0].Payload += "x"
	rep, err := newVerifier(e).Verify(context.Background(), owner, e.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid || !rep.File.OK || !rep.AuditChain.OK {
		t.Fatalf("report = %+v", rep)
	}
	bad := 0
	for _, s := range rep.Signatures {
		if !s.SealOK {
			bad++
		}
	}
	if bad != 1 {
		t.Errorf("bad seals = %d", bad)
	}
}

func TestVerifyDetectsSignatureForOtherDocumentHash(t *testing.T) {
	e := completed(t)
	e.sigs.Items[1].DocumentSHA256 = "0000"
	rep, err := newVerifier(e).Verify(context.Background(), owner, e.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid || rep.Signatures[1].HashMatchesDocument || !rep.Signatures[0].HashMatchesDocument {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyDetectsTamperedAuditEvent(t *testing.T) {
	e := completed(t)
	e.audit.Events[1].Actor = "someone-else"
	rep, err := newVerifier(e).Verify(context.Background(), owner, e.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid || rep.AuditChain.OK || rep.AuditChain.FirstBrokenEventID == nil || *rep.AuditChain.FirstBrokenEventID != e.audit.Events[1].ID {
		t.Fatalf("report = %+v", rep)
	}
	if !rep.File.OK {
		t.Error("file should still be ok")
	}
}

func TestVerifyIsOwnerScoped(t *testing.T) {
	e := completed(t)
	_, err := newVerifier(e).Verify(context.Background(), "22222222-2222-4222-8222-222222222222", e.doc.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := newVerifier(e).Verify(context.Background(), owner, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id err = %v", err)
	}
}

func TestVerifyDraftWithoutSignaturesIsValid(t *testing.T) {
	e := newLifeEnv()
	d := e.draft(t)
	se := &signEnv{lifeEnv: e, doc: d}
	rep, err := newVerifier(se).Verify(context.Background(), owner, d.ID)
	if err != nil || !rep.Valid || len(rep.Signatures) != 0 {
		t.Fatalf("report = %+v, err = %v", rep, err)
	}
}
