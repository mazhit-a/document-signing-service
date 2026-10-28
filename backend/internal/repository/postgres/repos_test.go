package postgres_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/notify/email"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres/pgtest"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

var (
	_ service.UserRepository      = (*postgres.UserRepository)(nil)
	_ service.DocumentRepository  = (*postgres.DocumentRepository)(nil)
	_ service.SignerRepository    = (*postgres.SignerRepository)(nil)
	_ service.SignatureRepository = (*postgres.SignatureRepository)(nil)
	_ service.AuditRepository     = (*postgres.AuditRepository)(nil)
	_ service.EmailOutbox         = (*postgres.EmailRepository)(nil)
	_ email.Store                 = (*postgres.EmailRepository)(nil)
)

type repos struct {
	db         *pgtest.DB
	tx         *postgres.TxManager
	users      *postgres.UserRepository
	documents  *postgres.DocumentRepository
	signers    *postgres.SignerRepository
	signatures *postgres.SignatureRepository
	audit      *postgres.AuditRepository
	email      *postgres.EmailRepository
	crypto     *email.Crypto
}

const testOutboxKey = "outbox-test-key-0123456789abcdef-xyz"

func newRepos(t *testing.T) *repos {
	t.Helper()
	db := pgtest.New(t)
	tx := postgres.NewTxManager(db.Pool)
	crypto, err := email.NewCrypto(testOutboxKey)
	if err != nil {
		t.Fatal(err)
	}
	return &repos{
		email:      postgres.NewEmailRepository(db.Pool, tx, crypto, service.UUIDGenerator{}.NewID),
		crypto:     crypto,
		db:         db,
		tx:         tx,
		users:      postgres.NewUserRepository(db.Pool),
		documents:  postgres.NewDocumentRepository(db.Pool, tx),
		signers:    postgres.NewSignerRepository(db.Pool),
		signatures: postgres.NewSignatureRepository(db.Pool),
		audit:      postgres.NewAuditRepository(db.Pool),
	}
}

func newID(t testing.TB) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

var baseTime = time.Date(2026, 3, 1, 12, 0, 0, 123456000, time.UTC)

func (r *repos) user(t *testing.T, email string) domain.User {
	t.Helper()
	u, err := r.users.Create(context.Background(), domain.User{ID: newID(t), Email: email, PasswordHash: "hash", FullName: "Name", CreatedAt: baseTime})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (r *repos) doc(t *testing.T, owner string, createdAt time.Time, status domain.DocumentStatus, signerCount int) domain.Document {
	t.Helper()
	d := domain.Document{
		ID: newID(t), OwnerID: owner, Title: "Contract", FileName: "c.pdf", ContentType: "application/pdf",
		SizeBytes: 10, StorageKey: newID(t), SHA256: strings.Repeat("a", 64), Status: status,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	for i := 1; i <= signerCount; i++ {
		d.Signers = append(d.Signers, domain.Signer{
			ID: newID(t), DocumentID: d.ID, Order: i, FullName: fmt.Sprintf("Signer %d", i),
			Email: fmt.Sprintf("s%d@example.com", i), Status: domain.SignerWaiting,
		})
	}
	if err := r.documents.Create(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestUserRepository(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	u := r.user(t, "Alice@Example.com")
	if u.Email != "alice@example.com" {
		t.Fatalf("email should be normalized, got %q", u.Email)
	}
	if !u.CreatedAt.Equal(baseTime) {
		t.Fatalf("created_at = %v, want %v", u.CreatedAt, baseTime)
	}

	_, err := r.users.Create(ctx, domain.User{ID: newID(t), Email: "ALICE@example.com", PasswordHash: "h", FullName: "Dup", CreatedAt: baseTime})
	if !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}

	byEmail, err := r.users.GetByEmail(ctx, "alice@EXAMPLE.com")
	if err != nil || byEmail.ID != u.ID || byEmail.PasswordHash != "hash" {
		t.Fatalf("GetByEmail = %+v, %v", byEmail, err)
	}
	byID, err := r.users.GetByID(ctx, u.ID)
	if err != nil || byID.Email != u.Email {
		t.Fatalf("GetByID = %+v, %v", byID, err)
	}

	if _, err := r.users.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := r.users.GetByID(ctx, newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := r.users.GetByID(ctx, "not-a-uuid"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound for malformed id, got %v", err)
	}
}

func TestDocumentCreateAndGet(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	other := r.user(t, "x@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 3)

	got, err := r.documents.GetForOwner(ctx, owner.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != d.Title || got.SHA256 != d.SHA256 || got.Status != domain.DocumentDraft || !got.CreatedAt.Equal(baseTime) {
		t.Fatalf("unexpected document %+v", got)
	}
	if len(got.Signers) != 3 {
		t.Fatalf("want 3 signers, got %d", len(got.Signers))
	}
	for i, s := range got.Signers {
		if s.Order != i+1 || s.DocumentID != d.ID || s.Status != domain.SignerWaiting || s.TokenHash != "" {
			t.Fatalf("unexpected signer %d: %+v", i, s)
		}
	}

	if _, err := r.documents.GetForOwner(ctx, other.ID, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign owner: want ErrNotFound, got %v", err)
	}
	if _, err := r.documents.GetForOwner(ctx, owner.ID, newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id: want ErrNotFound, got %v", err)
	}
	if _, err := r.documents.GetForOwner(ctx, owner.ID, "garbage"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("malformed id: want ErrNotFound, got %v", err)
	}
	byID, err := r.documents.GetByID(ctx, d.ID)
	if err != nil || byID.OwnerID != owner.ID {
		t.Fatalf("GetByID = %+v, %v", byID, err)
	}
}

func TestDocumentCreateIsAtomic(t *testing.T) {
	r := newRepos(t)
	owner := r.user(t, "o@example.com")
	d := domain.Document{
		ID: newID(t), OwnerID: owner.ID, Title: "T", FileName: "f", ContentType: "application/pdf",
		SizeBytes: 1, StorageKey: newID(t), SHA256: strings.Repeat("b", 64), Status: domain.DocumentDraft,
		CreatedAt: baseTime, UpdatedAt: baseTime,
		Signers: []domain.Signer{
			{ID: newID(t), Order: 1, FullName: "A", Email: "a@x.io", Status: domain.SignerWaiting},
			{ID: newID(t), Order: 1, FullName: "B", Email: "b@x.io", Status: domain.SignerWaiting},
		},
	}
	if err := r.documents.Create(context.Background(), d); err == nil {
		t.Fatal("expected duplicate order error")
	}
	var n int
	if err := r.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM documents`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("document should be rolled back, found %d", n)
	}
}

func TestDocumentKeysetPaginationWithEqualCreatedAt(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	other := r.user(t, "x@example.com")

	want := map[string]bool{}
	for i := 0; i < 7; i++ {
		ts := baseTime
		if i >= 4 {
			ts = baseTime.Add(time.Hour)
		}
		d := r.doc(t, owner.ID, ts, domain.DocumentDraft, 1)
		want[d.ID] = true
	}
	r.doc(t, other.ID, baseTime, domain.DocumentDraft, 1)

	var seen []domain.Document
	var cursor *service.DocumentCursor
	pages := 0
	for {
		page, err := r.documents.List(ctx, service.DocumentListParams{OwnerID: owner.ID, Limit: 3, After: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		seen = append(seen, page.Items...)
		if page.Next == nil {
			break
		}
		if len(page.Items) != 3 {
			t.Fatalf("non-final page has %d items", len(page.Items))
		}
		cursor = page.Next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pages != 3 {
		t.Fatalf("want 3 pages, got %d", pages)
	}
	if len(seen) != 7 {
		t.Fatalf("want 7 documents, got %d", len(seen))
	}
	ids := map[string]bool{}
	for i, d := range seen {
		if ids[d.ID] {
			t.Fatalf("duplicate document %s across pages", d.ID)
		}
		ids[d.ID] = true
		if !want[d.ID] {
			t.Fatalf("foreign document %s leaked", d.ID)
		}
		if i > 0 {
			prev := seen[i-1]
			ordered := prev.CreatedAt.After(d.CreatedAt) || (prev.CreatedAt.Equal(d.CreatedAt) && prev.ID > d.ID)
			if !ordered {
				t.Fatalf("order violated at %d: (%v,%s) then (%v,%s)", i, prev.CreatedAt, prev.ID, d.CreatedAt, d.ID)
			}
		}
		if len(d.Signers) != 1 {
			t.Fatalf("list should include signers, got %d", len(d.Signers))
		}
	}
}

func TestDocumentListExactMultipleHasNoNextCursor(t *testing.T) {
	r := newRepos(t)
	owner := r.user(t, "o@example.com")
	for i := 0; i < 4; i++ {
		r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 1)
	}
	page, err := r.documents.List(context.Background(), service.DocumentListParams{OwnerID: owner.ID, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 || page.Next != nil {
		t.Fatalf("items=%d next=%v", len(page.Items), page.Next)
	}
}

func TestDocumentListStatusFilter(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	statuses := []domain.DocumentStatus{domain.DocumentDraft, domain.DocumentInProgress, domain.DocumentInProgress, domain.DocumentCompleted}
	for i, s := range statuses {
		r.doc(t, owner.ID, baseTime.Add(time.Duration(i)*time.Minute), s, 1)
	}

	for _, tc := range []struct {
		status domain.DocumentStatus
		want   int
	}{
		{domain.DocumentDraft, 1},
		{domain.DocumentInProgress, 2},
		{domain.DocumentCompleted, 1},
		{domain.DocumentRejected, 0},
	} {
		st := tc.status
		page, err := r.documents.List(ctx, service.DocumentListParams{OwnerID: owner.ID, Status: &st, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != tc.want {
			t.Errorf("status %s: got %d, want %d", tc.status, len(page.Items), tc.want)
		}
		for _, d := range page.Items {
			if d.Status != tc.status {
				t.Errorf("status %s: got document with status %s", tc.status, d.Status)
			}
		}
	}

	inProgress := domain.DocumentInProgress
	first, err := r.documents.List(ctx, service.DocumentListParams{OwnerID: owner.ID, Status: &inProgress, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Next == nil {
		t.Fatalf("expected one item and a cursor, got %d / %v", len(first.Items), first.Next)
	}
	second, err := r.documents.List(ctx, service.DocumentListParams{OwnerID: owner.ID, Status: &inProgress, Limit: 1, After: first.Next})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Next != nil || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("unexpected second page: %+v", second)
	}
}

func TestDocumentListForeignOwnerIsEmpty(t *testing.T) {
	r := newRepos(t)
	owner := r.user(t, "o@example.com")
	other := r.user(t, "x@example.com")
	r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 1)
	page, err := r.documents.List(context.Background(), service.DocumentListParams{OwnerID: other.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.Next != nil {
		t.Fatalf("expected empty page, got %+v", page)
	}
}

func TestDocumentUpdateStatus(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 1)

	done := baseTime.Add(time.Hour)
	if err := r.documents.UpdateStatus(ctx, d.ID, domain.DocumentCompleted, done, &done); err != nil {
		t.Fatal(err)
	}
	got, err := r.documents.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DocumentCompleted || got.CompletedAt == nil || !got.CompletedAt.Equal(done) || !got.UpdatedAt.Equal(done) {
		t.Fatalf("unexpected document after update: %+v", got)
	}
	if err := r.documents.UpdateStatus(ctx, newID(t), domain.DocumentCompleted, done, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDocumentGetForUpdateBlocksSecondTx(t *testing.T) {
	r := newRepos(t)
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 1)

	firstLocked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- r.tx.WithinTx(context.Background(), func(ctx context.Context) error {
			if _, err := r.documents.GetForUpdate(ctx, d.ID); err != nil {
				return err
			}
			close(firstLocked)
			<-release
			return nil
		})
	}()
	<-firstLocked

	secondAcquired := make(chan time.Time, 1)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- r.tx.WithinTx(context.Background(), func(ctx context.Context) error {
			_, err := r.documents.GetForUpdate(ctx, d.ID)
			secondAcquired <- time.Now()
			return err
		})
	}()

	select {
	case <-secondAcquired:
		t.Fatal("second transaction acquired the lock while the first still holds it")
	case <-time.After(500 * time.Millisecond):
	}

	committedAt := time.Now()
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case at := <-secondAcquired:
		if at.Before(committedAt) {
			t.Fatal("second transaction acquired the lock before the first committed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second transaction never acquired the lock after commit")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestDocumentGetForUpdateNotFound(t *testing.T) {
	r := newRepos(t)
	err := r.tx.WithinTx(context.Background(), func(ctx context.Context) error {
		_, err := r.documents.GetForUpdate(ctx, newID(t))
		return err
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSignerRepository(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 2)
	first, second := d.Signers[0], d.Signers[1]

	list, err := r.signers.ListByDocument(ctx, d.ID)
	if err != nil || len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("ListByDocument = %+v, %v", list, err)
	}

	if _, err := r.signers.GetByTokenHash(ctx, "deadbeef"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := r.signers.GetByTokenHash(ctx, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty hash must not match signers without token, got %v", err)
	}

	hash := integrity.HashToken("token-1")
	if err := r.signers.SetTokenHash(ctx, first.ID, hash); err != nil {
		t.Fatal(err)
	}
	byToken, err := r.signers.GetByTokenHash(ctx, hash)
	if err != nil || byToken.ID != first.ID || byToken.TokenHash != hash {
		t.Fatalf("GetByTokenHash = %+v, %v", byToken, err)
	}
	if err := r.signers.SetTokenHash(ctx, second.ID, hash); err == nil {
		t.Fatal("duplicate token hash should fail")
	}

	decided := baseTime.Add(time.Minute)
	if err := r.signers.UpdateStatus(ctx, first.ID, domain.SignerRejected, &decided, "not agreed"); err != nil {
		t.Fatal(err)
	}
	got, err := r.signers.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.SignerRejected || got.RejectReason != "not agreed" || got.DecidedAt == nil || !got.DecidedAt.Equal(decided) {
		t.Fatalf("unexpected signer: %+v", got)
	}

	if err := r.signers.UpdateStatus(ctx, second.ID, domain.SignerPending, nil, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = r.signers.GetByID(ctx, second.ID)
	if got.Status != domain.SignerPending || got.DecidedAt != nil || got.RejectReason != "" {
		t.Fatalf("unexpected signer: %+v", got)
	}

	if err := r.signers.UpdateStatus(ctx, newID(t), domain.SignerSigned, &decided, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := r.signers.GetByID(ctx, newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSignatureRepository(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentInProgress, 2)

	sig := func(signerID, ip, ua string) domain.Signature {
		return domain.Signature{
			ID: newID(t), SignerID: signerID, DocumentID: d.ID, DocumentSHA256: d.SHA256,
			Payload: "payload-" + signerID, Seal: "seal", SignedAt: baseTime.Add(time.Minute), IP: ip, UserAgent: ua,
		}
	}
	second := sig(d.Signers[1].ID, "", "")
	first := sig(d.Signers[0].ID, "203.0.113.7", "curl/8")
	if err := r.signatures.Insert(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := r.signatures.Insert(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := r.signatures.Insert(ctx, sig(d.Signers[0].ID, "", "")); err == nil {
		t.Fatal("second signature for the same signer must fail")
	}

	list, err := r.signatures.ListByDocument(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("signatures should be ordered by signer order: %+v", list)
	}
	if list[0].IP != "203.0.113.7" || list[0].UserAgent != "curl/8" || list[1].IP != "" || list[1].UserAgent != "" {
		t.Fatalf("ip/user agent round trip failed: %+v", list)
	}
	if list[0].Payload != first.Payload || list[0].DocumentSHA256 != d.SHA256 || !list[0].SignedAt.Equal(first.SignedAt) {
		t.Fatalf("unexpected signature: %+v", list[0])
	}

	got, err := r.signatures.GetBySigner(ctx, d.Signers[1].ID)
	if err != nil || got.ID != second.ID {
		t.Fatalf("GetBySigner = %+v, %v", got, err)
	}
	empty := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 1)
	if _, err := r.signatures.GetBySigner(ctx, empty.Signers[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAuditRepositoryAppendBuildsVerifiableChain(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 1)
	other := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 1)

	steps := []struct {
		typ   domain.EventType
		actor string
		data  map[string]any
	}{
		{domain.EventDocumentCreated, "user:" + owner.ID, map[string]any{"signers": 1, "title": "Contract"}},
		{domain.EventDocumentSent, "user:" + owner.ID, nil},
		{domain.EventSignerApproved, "signer:x", map[string]any{"order": 1}},
	}
	for i, s := range steps {
		ev, err := r.audit.Append(ctx, d.ID, s.typ, s.actor, s.data, baseTime.Add(time.Duration(i)*time.Second+789*time.Nanosecond))
		if err != nil {
			t.Fatal(err)
		}
		if ev.ID == 0 || ev.Hash == "" {
			t.Fatalf("unexpected event %+v", ev)
		}
		if i == 0 && ev.PrevHash != integrity.GenesisHash {
			t.Fatalf("first event prev hash = %s", ev.PrevHash)
		}
	}
	if _, err := r.audit.Append(ctx, other.ID, domain.EventDocumentCreated, "user:x", nil, baseTime); err != nil {
		t.Fatal(err)
	}

	events, err := r.audit.ListByDocument(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	chain := make([]integrity.Event, len(events))
	for i, e := range events {
		if i > 0 && e.PrevHash != events[i-1].Hash {
			t.Fatalf("event %d prev hash does not link to event %d", i, i-1)
		}
		if e.CreatedAt.Nanosecond()%1000 != 0 {
			t.Fatalf("created_at not truncated to microseconds: %v", e.CreatedAt)
		}
		chain[i] = integrity.Event{ID: e.ID, Type: string(e.Type), Actor: e.Actor, Data: e.Data, CreatedAt: e.CreatedAt, PrevHash: e.PrevHash, Hash: e.Hash}
	}
	if report := integrity.VerifyChain(chain); !report.OK || report.Events != 3 {
		t.Fatalf("chain read back from Postgres must verify: %+v", report)
	}

	otherEvents, err := r.audit.ListByDocument(ctx, other.ID)
	if err != nil || len(otherEvents) != 1 || otherEvents[0].PrevHash != integrity.GenesisHash {
		t.Fatalf("chains must be per document: %+v, %v", otherEvents, err)
	}
}

func TestAuditAppendRollsBackWithTransaction(t *testing.T) {
	r := newRepos(t)
	ctx := context.Background()
	owner := r.user(t, "o@example.com")
	d := r.doc(t, owner.ID, baseTime, domain.DocumentDraft, 1)
	boom := errors.New("boom")

	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := r.audit.Append(ctx, d.ID, domain.EventDocumentCreated, "a", nil, baseTime); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	events, err := r.audit.ListByDocument(ctx, d.ID)
	if err != nil || len(events) != 0 {
		t.Fatalf("event should be rolled back: %+v, %v", events, err)
	}
}

func TestAuditAppendUnknownDocument(t *testing.T) {
	r := newRepos(t)
	if _, err := r.audit.Append(context.Background(), "nope", domain.EventDocumentCreated, "a", nil, baseTime); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
