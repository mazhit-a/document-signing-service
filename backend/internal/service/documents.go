package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/storage"
)

const (
	DefaultListLimit = 20
	MaxListLimit     = 100
	MaxFileNameLen   = 255
	sniffLen         = 512
)

type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type fileKind struct {
	contentType string
	sniffOK     func(sniffed string) bool
}

var allowedFiles = map[string]fileKind{
	".pdf": {
		contentType: "application/pdf",
		sniffOK:     func(s string) bool { return s == "application/pdf" },
	},
	".docx": {
		contentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		sniffOK:     func(s string) bool { return s == "application/zip" },
	},
	".txt": {
		contentType: "text/plain",
		sniffOK:     func(s string) bool { return strings.HasPrefix(s, "text/plain") },
	},
}

type CreateDocumentInput struct {
	OwnerID  string
	Title    string
	FileName string
	File     io.Reader
	Signers  []domain.SignerInput
}

type DocumentDetail struct {
	Document   domain.Document
	Signatures []domain.Signature
}

type ListDocumentsInput struct {
	OwnerID string
	Status  *domain.DocumentStatus
	Limit   int
	After   *DocumentCursor
}

type DocumentService struct {
	docs       DocumentRepository
	signatures SignatureRepository
	audit      AuditRepository
	files      storage.FileStorage
	tx         TxManager
	clock      Clock
	ids        IDGenerator
	maxBytes   int64
}

func NewDocumentService(docs DocumentRepository, signatures SignatureRepository, audit AuditRepository, files storage.FileStorage, tx TxManager, clock Clock, ids IDGenerator, maxBytes int64) *DocumentService {
	return &DocumentService{docs: docs, signatures: signatures, audit: audit, files: files, tx: tx, clock: clock, ids: ids, maxBytes: maxBytes}
}

func (s *DocumentService) MaxUploadBytes() int64 { return s.maxBytes }

func (s *DocumentService) CreateDocument(ctx context.Context, in CreateDocumentInput) (domain.Document, error) {
	fileName := cleanFileName(in.FileName)
	fields := map[string]string{}
	if err := domain.ValidateTitle(in.Title); err != nil {
		mergeFields(fields, err)
	}
	if err := domain.ValidateSignerInputs(in.Signers); err != nil {
		mergeFields(fields, err)
	}
	if in.File == nil || fileName == "" {
		fields["file"] = "required"
	}
	if len(fields) > 0 {
		return domain.Document{}, domain.NewValidationError(fields)
	}

	kind, ok := allowedFiles[strings.ToLower(path.Ext(fileName))]
	if !ok {
		return domain.Document{}, domain.ErrUnsupportedFileType
	}

	br := bufio.NewReaderSize(in.File, sniffLen)
	head, err := br.Peek(sniffLen)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return domain.Document{}, mapReadError(err)
	}
	if len(head) == 0 {
		return domain.Document{}, domain.NewValidationError(map[string]string{"file": "must not be empty"})
	}
	if !kind.sniffOK(http.DetectContentType(head)) {
		return domain.Document{}, domain.ErrUnsupportedFileType
	}

	docID := s.ids.NewID()
	signers, err := domain.NewSigners(docID, in.Signers, s.ids.NewID)
	if err != nil {
		return domain.Document{}, err
	}

	hr := integrity.NewHashingReader(&maxReader{r: br, max: s.maxBytes})
	key, size, err := s.files.Put(ctx, hr)
	if err != nil {
		return domain.Document{}, mapReadError(err)
	}

	now := s.clock.Now().UTC().Truncate(time.Microsecond)
	doc := domain.Document{
		ID:          docID,
		OwnerID:     in.OwnerID,
		Title:       strings.TrimSpace(in.Title),
		FileName:    fileName,
		ContentType: kind.contentType,
		SizeBytes:   size,
		StorageKey:  key,
		SHA256:      hr.Sum(),
		Status:      domain.DocumentDraft,
		CreatedAt:   now,
		UpdatedAt:   now,
		Signers:     signers,
	}

	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.docs.Create(ctx, doc); err != nil {
			return err
		}
		_, err := s.audit.Append(ctx, doc.ID, domain.EventDocumentCreated, in.OwnerID, map[string]any{
			"title":        doc.Title,
			"file_name":    doc.FileName,
			"size_bytes":   doc.SizeBytes,
			"sha256":       doc.SHA256,
			"signer_count": len(doc.Signers),
		}, now)
		return err
	})
	if err != nil {
		_ = s.files.Delete(context.WithoutCancel(ctx), key)
		return domain.Document{}, err
	}
	return doc, nil
}

func (s *DocumentService) ListDocuments(ctx context.Context, in ListDocumentsInput) (DocumentPage, error) {
	if in.Status != nil && !in.Status.Valid() {
		return DocumentPage{}, domain.NewValidationError(map[string]string{"status": "unknown status"})
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultListLimit
	}
	if limit < 1 || limit > MaxListLimit {
		return DocumentPage{}, domain.NewValidationError(map[string]string{"limit": fmt.Sprintf("must be between 1 and %d", MaxListLimit)})
	}
	return s.docs.List(ctx, DocumentListParams{OwnerID: in.OwnerID, Status: in.Status, Limit: limit, After: in.After})
}

func (s *DocumentService) GetDocument(ctx context.Context, ownerID, id string) (DocumentDetail, error) {
	doc, err := s.docs.GetForOwner(ctx, ownerID, id)
	if err != nil {
		return DocumentDetail{}, err
	}
	sigs, err := s.signatures.ListByDocument(ctx, doc.ID)
	if err != nil {
		return DocumentDetail{}, err
	}
	return DocumentDetail{Document: doc, Signatures: sigs}, nil
}

func (s *DocumentService) OpenFile(ctx context.Context, ownerID, id string) (domain.Document, io.ReadCloser, error) {
	doc, err := s.docs.GetForOwner(ctx, ownerID, id)
	if err != nil {
		return domain.Document{}, nil, err
	}
	rc, err := s.files.Open(ctx, doc.StorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return domain.Document{}, nil, fmt.Errorf("stored file missing for document %s: %w", doc.ID, err)
		}
		return domain.Document{}, nil, err
	}
	return doc, rc, nil
}

type maxReader struct {
	r    io.Reader
	max  int64
	seen int64
}

func (m *maxReader) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	m.seen += int64(n)
	if m.seen > m.max {
		return n, domain.ErrFileTooLarge
	}
	return n, err
}

func mapReadError(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return domain.ErrFileTooLarge
	}
	return err
}

func mergeFields(dst map[string]string, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		for k, v := range ve.Fields {
			dst[k] = v
		}
	}
}

func cleanFileName(raw string) string {
	if i := strings.LastIndexAny(raw, `/\`); i >= 0 {
		raw = raw[i+1:]
	}
	raw = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	raw = strings.TrimSpace(raw)
	if raw == "." || raw == ".." {
		return ""
	}
	for utf8.RuneCountInString(raw) > MaxFileNameLen {
		_, sz := utf8.DecodeLastRuneInString(raw)
		raw = raw[:len(raw)-sz]
	}
	return raw
}
