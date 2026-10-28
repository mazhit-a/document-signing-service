package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

const owner = "11111111-1111-4111-8111-111111111111"

var fixedNow = time.Date(2026, 10, 28, 5, 12, 3, 123456789, time.UTC)

type env struct {
	svc   *service.DocumentService
	docs  *fakes.Docs
	sigs  *fakes.Signatures
	audit *fakes.Audit
	files *fakes.Storage
	clock *fakes.Clock
}

func newEnv(maxBytes int64) *env {
	e := &env{docs: fakes.NewDocs(), sigs: &fakes.Signatures{}, audit: &fakes.Audit{}, files: fakes.NewStorage(), clock: &fakes.Clock{T: fixedNow}}
	e.svc = service.NewDocumentService(e.docs, e.sigs, e.audit, e.files, fakes.Tx{}, e.clock, &fakes.SeqIDs{}, maxBytes)
	return e
}

var twoSigners = []domain.SignerInput{
	{FullName: "A. Seitov", Email: "a@x.kz"},
	{FullName: "HR Manager", Email: "hr@x.kz"},
}

func pdfBytes(n int) []byte {
	return append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), n)...)
}

func (e *env) create(name, title string, body []byte, signers []domain.SignerInput) (domain.Document, error) {
	return e.svc.CreateDocument(context.Background(), service.CreateDocumentInput{
		OwnerID: owner, Title: title, FileName: name, File: bytes.NewReader(body), Signers: signers,
	})
}

func TestCreateDocumentSuccess(t *testing.T) {
	e := newEnv(1 << 20)
	body := pdfBytes(1000)
	doc, err := e.create("contract.pdf", "  Contract  ", body, twoSigners)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if doc.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 mismatch: %s", doc.SHA256)
	}
	if doc.SizeBytes != int64(len(body)) || doc.Title != "Contract" || doc.Status != domain.DocumentDraft {
		t.Errorf("unexpected document: %+v", doc)
	}
	if doc.ContentType != "application/pdf" || doc.FileName != "contract.pdf" {
		t.Errorf("unexpected file meta: %+v", doc)
	}
	if !doc.CreatedAt.Equal(fixedNow.Truncate(time.Microsecond)) {
		t.Errorf("created_at not truncated: %v", doc.CreatedAt)
	}
	if len(doc.Signers) != 2 || doc.Signers[0].Order != 1 || doc.Signers[1].Order != 2 || doc.Signers[0].Status != domain.SignerWaiting {
		t.Errorf("unexpected signers: %+v", doc.Signers)
	}
	if _, ok := e.docs.Items[doc.ID]; !ok {
		t.Error("document not persisted")
	}
	if stored := e.files.Files[doc.StorageKey]; !bytes.Equal(stored, body) {
		t.Error("stored bytes differ")
	}
	if len(e.audit.Events) != 1 || e.audit.Events[0].Type != domain.EventDocumentCreated || e.audit.Events[0].Actor != owner {
		t.Errorf("audit events: %+v", e.audit.Events)
	}
}

func TestCreateDocumentFileTypes(t *testing.T) {
	zipBytes := append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0}, 100)...)
	tests := []struct {
		name    string
		file    string
		body    []byte
		wantErr error
	}{
		{"pdf ok", "a.pdf", pdfBytes(10), nil},
		{"docx ok", "a.DOCX", zipBytes, nil},
		{"txt ok", "a.txt", []byte("hello world"), nil},
		{"exe extension", "a.exe", []byte("hello"), domain.ErrUnsupportedFileType},
		{"no extension", "a", []byte("hello"), domain.ErrUnsupportedFileType},
		{"pdf extension with text body", "a.pdf", []byte("just text"), domain.ErrUnsupportedFileType},
		{"txt extension with binary body", "a.txt", []byte{0x00, 0x01, 0x02, 0xff, 0xfe}, domain.ErrUnsupportedFileType},
		{"docx extension with pdf body", "a.docx", pdfBytes(10), domain.ErrUnsupportedFileType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(1 << 20)
			_, err := e.create(tt.file, "T", tt.body, twoSigners)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil && e.files.Count() != 0 {
				t.Error("file stored despite rejection")
			}
		})
	}
}

func TestCreateDocumentValidation(t *testing.T) {
	long := strings.Repeat("t", 201)
	tests := []struct {
		name    string
		title   string
		file    string
		body    []byte
		signers []domain.SignerInput
		field   string
	}{
		{"empty title", "", "a.pdf", pdfBytes(5), twoSigners, "title"},
		{"long title", long, "a.pdf", pdfBytes(5), twoSigners, "title"},
		{"no signers", "T", "a.pdf", pdfBytes(5), nil, "signers"},
		{"eleven signers", "T", "a.pdf", pdfBytes(5), manySigners(11), "signers"},
		{"bad email", "T", "a.pdf", pdfBytes(5), []domain.SignerInput{{FullName: "A", Email: "nope"}}, "signers[0].email"},
		{"duplicate email", "T", "a.pdf", pdfBytes(5), []domain.SignerInput{{FullName: "A", Email: "a@x.kz"}, {FullName: "B", Email: "A@x.kz"}}, "signers[1].email"},
		{"empty file", "T", "a.txt", nil, twoSigners, "file"},
		{"missing name", "T", "", pdfBytes(5), twoSigners, "file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(1 << 20)
			_, err := e.create(tt.file, tt.title, tt.body, tt.signers)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want validation error", err)
			}
			if _, ok := ve.Fields[tt.field]; !ok {
				t.Errorf("fields %v missing %q", ve.Fields, tt.field)
			}
			if e.files.Count() != 0 || len(e.docs.Items) != 0 {
				t.Error("state changed despite validation failure")
			}
		})
	}
}

func manySigners(n int) []domain.SignerInput {
	out := make([]domain.SignerInput, n)
	for i := range out {
		out[i] = domain.SignerInput{FullName: "S", Email: string(rune('a'+i)) + "@x.kz"}
	}
	return out
}

func TestCreateDocumentTooLarge(t *testing.T) {
	e := newEnv(1000)
	_, err := e.create("a.pdf", "T", pdfBytes(5000), twoSigners)
	if !errors.Is(err, domain.ErrFileTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if e.files.Count() != 0 || len(e.docs.Items) != 0 {
		t.Error("state changed")
	}
	if _, err := e.create("a.pdf", "T", pdfBytes(900), twoSigners); err != nil {
		t.Errorf("file under the limit rejected: %v", err)
	}
}

func TestCreateDocumentRollsBackFileOnDBFailure(t *testing.T) {
	e := newEnv(1 << 20)
	boom := errors.New("db down")
	e.docs.CreateFn = func() error { return boom }
	if _, err := e.create("a.pdf", "T", pdfBytes(10), twoSigners); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if e.files.Count() != 0 {
		t.Error("orphan file left in storage")
	}
}

func TestCreateDocumentRollsBackFileOnAuditFailure(t *testing.T) {
	e := newEnv(1 << 20)
	e.audit.Err = errors.New("audit down")
	if _, err := e.create("a.pdf", "T", pdfBytes(10), twoSigners); err == nil {
		t.Fatal("expected error")
	}
	if e.files.Count() != 0 {
		t.Error("orphan file left in storage")
	}
}

func TestCreateDocumentSanitizesFileName(t *testing.T) {
	e := newEnv(1 << 20)
	doc, err := e.create("..\\..\\etc/evil\r\n.pdf", "T", pdfBytes(10), twoSigners)
	if err != nil {
		t.Fatal(err)
	}
	if doc.FileName != "evil.pdf" {
		t.Errorf("file name = %q", doc.FileName)
	}
}

type spyReader struct {
	remaining int
	maxAsk    int
}

func (s *spyReader) Read(p []byte) (int, error) {
	if len(p) > s.maxAsk {
		s.maxAsk = len(p)
	}
	if s.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > s.remaining {
		n = s.remaining
	}
	copy(p, bytes.Repeat([]byte("a"), n))
	s.remaining -= n
	return n, nil
}

func TestCreateDocumentStreamsWithoutBuffering(t *testing.T) {
	e := newEnv(16 << 20)
	src := &spyReader{remaining: 8 << 20}
	_, err := e.svc.CreateDocument(context.Background(), service.CreateDocumentInput{
		OwnerID: owner, Title: "Big", FileName: "big.txt", File: src, Signers: twoSigners,
	})
	if err != nil {
		t.Fatal(err)
	}
	if src.maxAsk > 64*1024 {
		t.Errorf("service asked for %d bytes at once; expected chunked streaming", src.maxAsk)
	}
}

func TestListDocumentsPaginatesWithoutDuplicates(t *testing.T) {
	e := newEnv(1 << 20)
	for i := 0; i < 5; i++ {
		e.clock.T = fixedNow.Add(time.Duration(i/2) * time.Second)
		if _, err := e.create("a.txt", "T", []byte("hi"), twoSigners); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	var after *service.DocumentCursor
	pages := 0
	for {
		page, err := e.svc.ListDocuments(context.Background(), service.ListDocumentsInput{OwnerID: owner, Limit: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, d := range page.Items {
			if seen[d.ID] {
				t.Fatalf("duplicate %s", d.ID)
			}
			seen[d.ID] = true
		}
		if page.Next == nil {
			break
		}
		after = page.Next
	}
	if len(seen) != 5 || pages != 3 {
		t.Errorf("seen=%d pages=%d", len(seen), pages)
	}
}

func TestListDocumentsValidation(t *testing.T) {
	e := newEnv(1 << 20)
	bad := domain.DocumentStatus("bogus")
	tests := []struct {
		name  string
		in    service.ListDocumentsInput
		field string
	}{
		{"bad status", service.ListDocumentsInput{OwnerID: owner, Status: &bad}, "status"},
		{"negative limit", service.ListDocumentsInput{OwnerID: owner, Limit: -1}, "limit"},
		{"huge limit", service.ListDocumentsInput{OwnerID: owner, Limit: 101}, "limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.ListDocuments(context.Background(), tt.in)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) || ve.Fields[tt.field] == "" {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestGetDocumentAndOpenFileOwnership(t *testing.T) {
	e := newEnv(1 << 20)
	body := []byte("hello")
	doc, err := e.create("a.txt", "T", body, twoSigners)
	if err != nil {
		t.Fatal(err)
	}
	e.sigs.Items = append(e.sigs.Items, domain.Signature{ID: "s1", DocumentID: doc.ID, SignerID: doc.Signers[0].ID})

	detail, err := e.svc.GetDocument(context.Background(), owner, doc.ID)
	if err != nil || len(detail.Signatures) != 1 || detail.Document.ID != doc.ID {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	if _, err := e.svc.GetDocument(context.Background(), "someone-else", doc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("foreign get err = %v", err)
	}
	if _, _, err := e.svc.OpenFile(context.Background(), "someone-else", doc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("foreign open err = %v", err)
	}
	if _, _, err := e.svc.OpenFile(context.Background(), owner, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown open err = %v", err)
	}
	_, rc, err := e.svc.OpenFile(context.Background(), owner, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, body) {
		t.Error("downloaded bytes differ")
	}
}
