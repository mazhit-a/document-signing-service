package postgres_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

type verifyEnv struct {
	r      *repos
	files  *fakes.Storage
	owner  domain.User
	doc    domain.Document
	key    string
	verify *service.VerifyService
}

func completedForVerify(t *testing.T) *verifyEnv {
	t.Helper()
	r := newRepos(t)
	ctx := context.Background()
	files := fakes.NewStorage()
	content := []byte("%PDF-1.4 verify me")
	key, size, err := files.Put(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	owner := r.user(t, "verify-owner@example.com")
	d := domain.Document{
		ID: newID(t), OwnerID: owner.ID, Title: "Contract", FileName: "c.pdf", ContentType: "application/pdf",
		SizeBytes: size, StorageKey: key, SHA256: integrity.SHA256Hex(content), Status: domain.DocumentDraft,
		CreatedAt: baseTime, UpdatedAt: baseTime,
	}
	for i := 1; i <= 2; i++ {
		d.Signers = append(d.Signers, domain.Signer{
			ID: newID(t), DocumentID: d.ID, Order: i, FullName: "Signer", Email: "s@example.com", Status: domain.SignerWaiting,
		})
	}
	if err := r.documents.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	stored, err := r.documents.GetForOwner(ctx, owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}

	res, err := r.lifecycle(r.audit).SendDocument(ctx, owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	sign := service.NewSigningService(r.documents, r.signers, r.signatures, r.audit, files, r.tx, service.SystemClock{}, service.UUIDGenerator{}, notify.Noop{}, testSealSecret)
	for _, l := range res.Links {
		tok := l.URL[len("http://web.test/sign/"):]
		if _, err := sign.Approve(ctx, tok, service.ClientMeta{IP: "203.0.113.7", UserAgent: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	return &verifyEnv{
		r: r, files: files, owner: owner, doc: stored, key: key,
		verify: service.NewVerifyService(r.documents, r.signatures, r.audit, files, r.tx, service.SystemClock{}, testSealSecret),
	}
}

func (e *verifyEnv) run(t *testing.T) service.VerifyReport {
	t.Helper()
	rep, err := e.verify.Verify(context.Background(), e.owner.ID, e.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestVerifyUntouchedCompletedDocumentIsValid(t *testing.T) {
	e := completedForVerify(t)
	rep := e.run(t)
	if !rep.Valid || !rep.File.OK || !rep.AuditChain.OK || len(rep.Signatures) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	for _, s := range rep.Signatures {
		if !s.SealOK || !s.HashMatchesDocument {
			t.Errorf("signature = %+v", s)
		}
	}
}

func TestVerifyRepeatedCallsKeepChainValid(t *testing.T) {
	e := completedForVerify(t)
	for i := 0; i < 3; i++ {
		rep := e.run(t)
		if !rep.Valid || rep.AuditChain.Events != 4+i {
			t.Fatalf("call %d: %+v", i, rep)
		}
	}
	if n := e.r.countWhere(t, `SELECT count(*) FROM audit_events WHERE document_id = $1::uuid AND type = 'document.verified'`, e.doc.ID); n != 3 {
		t.Errorf("verified events = %d", n)
	}
}

func TestVerifyDetectsTamperedStoredFile(t *testing.T) {
	e := completedForVerify(t)
	e.files.Files[e.key] = []byte("%PDF-1.4 changed")
	rep := e.run(t)
	if rep.Valid || rep.File.OK || !rep.AuditChain.OK {
		t.Fatalf("report = %+v", rep)
	}
	for _, s := range rep.Signatures {
		if !s.SealOK {
			t.Errorf("signature should be intact: %+v", s)
		}
	}
}

func TestVerifyMissingFileIsReportedNot500(t *testing.T) {
	e := completedForVerify(t)
	delete(e.files.Files, e.key)
	rep := e.run(t)
	if rep.Valid || rep.File.OK {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyDetectsTamperedSignaturePayload(t *testing.T) {
	e := completedForVerify(t)
	if _, err := e.r.db.Pool.Exec(context.Background(),
		`UPDATE signatures SET payload = payload || 'x' WHERE id = (SELECT id FROM signatures WHERE document_id = $1::uuid ORDER BY signed_at LIMIT 1)`, e.doc.ID); err != nil {
		t.Fatal(err)
	}
	rep := e.run(t)
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

func TestVerifyDetectsTamperedAuditEvent(t *testing.T) {
	e := completedForVerify(t)
	ctx := context.Background()
	events, err := e.r.audit.ListByDocument(ctx, e.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := events[2]
	if _, err := e.r.db.Pool.Exec(ctx, `ALTER TABLE audit_events DISABLE TRIGGER audit_events_no_update_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.db.Pool.Exec(ctx, `UPDATE audit_events SET actor = 'attacker' WHERE id = $1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.db.Pool.Exec(ctx, `ALTER TABLE audit_events ENABLE TRIGGER audit_events_no_update_delete`); err != nil {
		t.Fatal(err)
	}

	rep := e.run(t)
	if rep.Valid || rep.AuditChain.OK || rep.AuditChain.FirstBrokenEventID == nil || *rep.AuditChain.FirstBrokenEventID != target.ID {
		t.Fatalf("report = %+v", rep)
	}
	if !rep.File.OK {
		t.Error("file should still be ok")
	}
}

func TestAuditTriggerBlocksTamperingWithoutTestBypass(t *testing.T) {
	e := completedForVerify(t)
	_, err := e.r.db.Pool.Exec(context.Background(), `UPDATE audit_events SET actor = 'attacker' WHERE document_id = $1::uuid`, e.doc.ID)
	if err == nil {
		t.Fatal("expected append-only trigger to reject update")
	}
}
