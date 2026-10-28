package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

const (
	multipartMemory   = 1 << 20
	multipartOverhead = 1 << 20
)

type DocumentUseCase interface {
	CreateDocument(ctx context.Context, in service.CreateDocumentInput) (domain.Document, error)
	ListDocuments(ctx context.Context, in service.ListDocumentsInput) (service.DocumentPage, error)
	GetDocument(ctx context.Context, ownerID, id string) (service.DocumentDetail, error)
	OpenFile(ctx context.Context, ownerID, id string) (domain.Document, io.ReadCloser, error)
	MaxUploadBytes() int64
}

type DocumentHandlers struct {
	uc DocumentUseCase
}

func NewDocumentHandlers(uc DocumentUseCase) *DocumentHandlers {
	return &DocumentHandlers{uc: uc}
}

type fileResponse struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

type signerResponse struct {
	ID           string `json:"id"`
	Order        int    `json:"order"`
	FullName     string `json:"full_name"`
	Email        string `json:"email"`
	Status       string `json:"status"`
	DecidedAt    string `json:"decided_at,omitempty"`
	RejectReason string `json:"reject_reason,omitempty"`
}

type documentResponse struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Status    string           `json:"status"`
	File      fileResponse     `json:"file"`
	Signers   []signerResponse `json:"signers"`
	CreatedAt string           `json:"created_at"`
}

type signatureResponse struct {
	ID             string `json:"id"`
	SignerID       string `json:"signer_id"`
	DocumentSHA256 string `json:"document_sha256"`
	Seal           string `json:"seal"`
	SignedAt       string `json:"signed_at"`
}

type documentDetailResponse struct {
	documentResponse
	UpdatedAt   string              `json:"updated_at"`
	CompletedAt string              `json:"completed_at,omitempty"`
	Signatures  []signatureResponse `json:"signatures"`
}

type documentListResponse struct {
	Items      []documentResponse `json:"items"`
	NextCursor *string            `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func toDocumentResponse(d domain.Document) documentResponse {
	signers := make([]signerResponse, len(d.Signers))
	for i, s := range d.Signers {
		signers[i] = signerResponse{
			ID: s.ID, Order: s.Order, FullName: s.FullName, Email: s.Email,
			Status: string(s.Status), RejectReason: s.RejectReason,
		}
		if s.DecidedAt != nil {
			signers[i].DecidedAt = formatTime(*s.DecidedAt)
		}
	}
	return documentResponse{
		ID:     d.ID,
		Title:  d.Title,
		Status: string(d.Status),
		File: fileResponse{
			Name: d.FileName, ContentType: d.ContentType, SizeBytes: d.SizeBytes, SHA256: d.SHA256,
		},
		Signers:   signers,
		CreatedAt: formatTime(d.CreatedAt),
	}
}

func toDetailResponse(d service.DocumentDetail) documentDetailResponse {
	resp := documentDetailResponse{
		documentResponse: toDocumentResponse(d.Document),
		UpdatedAt:        formatTime(d.Document.UpdatedAt),
		Signatures:       make([]signatureResponse, len(d.Signatures)),
	}
	if d.Document.CompletedAt != nil {
		resp.CompletedAt = formatTime(*d.Document.CompletedAt)
	}
	for i, s := range d.Signatures {
		resp.Signatures[i] = signatureResponse{
			ID: s.ID, SignerID: s.SignerID, DocumentSHA256: s.DocumentSHA256,
			Seal: s.Seal, SignedAt: formatTime(s.SignedAt),
		}
	}
	return resp
}

type signerInputDTO struct {
	FullName string `json:"full_name"`
	Email    string `json:"email"`
}

func parseSigners(raw string) ([]domain.SignerInput, error) {
	invalid := domain.NewValidationError(map[string]string{"signers": `must be a JSON array of {"full_name","email"}`})
	if strings.TrimSpace(raw) == "" {
		return nil, domain.NewValidationError(map[string]string{"signers": "required"})
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var dtos []signerInputDTO
	if err := dec.Decode(&dtos); err != nil {
		return nil, invalid
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid
	}
	out := make([]domain.SignerInput, len(dtos))
	for i, d := range dtos {
		out[i] = domain.SignerInput{FullName: d.FullName, Email: d.Email}
	}
	return out, nil
}

func (h *DocumentHandlers) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.uc.MaxUploadBytes()+multipartOverhead)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, r, domain.ErrFileTooLarge)
			return
		}
		WriteError(w, r, domain.ErrBadRequest)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	signers, err := parseSigners(r.FormValue("signers"))
	if err != nil {
		WriteError(w, r, err)
		return
	}

	in := service.CreateDocumentInput{
		OwnerID: UserIDFrom(r.Context()),
		Title:   r.FormValue("title"),
		Signers: signers,
	}
	file, header, ferr := r.FormFile("file")
	if ferr == nil {
		defer func() { _ = file.Close() }()
		in.File = file
		in.FileName = header.Filename
	}

	doc, err := h.uc.CreateDocument(r.Context(), in)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, toDocumentResponse(doc))
}

type cursorPayload struct {
	T string `json:"t"`
	I string `json:"i"`
}

func encodeCursor(c service.DocumentCursor) string {
	b, _ := json.Marshal(cursorPayload{T: c.CreatedAt.UTC().Format(time.RFC3339Nano), I: c.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(raw string) (*service.DocumentCursor, error) {
	bad := domain.NewValidationError(map[string]string{"cursor": "invalid"})
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, bad
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, bad
	}
	t, err := time.Parse(time.RFC3339Nano, p.T)
	if err != nil || p.I == "" {
		return nil, bad
	}
	return &service.DocumentCursor{CreatedAt: t, ID: p.I}, nil
}

func (h *DocumentHandlers) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := service.ListDocumentsInput{OwnerID: UserIDFrom(r.Context())}
	if s := q.Get("status"); s != "" {
		st := domain.DocumentStatus(s)
		in.Status = &st
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil {
			WriteError(w, r, domain.NewValidationError(map[string]string{"limit": "must be an integer"}))
			return
		}
		if n == 0 {
			n = -1
		}
		in.Limit = n
	}
	if c := q.Get("cursor"); c != "" {
		cur, err := decodeCursor(c)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		in.After = cur
	}
	page, err := h.uc.ListDocuments(r.Context(), in)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	resp := documentListResponse{Items: make([]documentResponse, len(page.Items))}
	for i, d := range page.Items {
		resp.Items[i] = toDocumentResponse(d)
	}
	if page.Next != nil {
		c := encodeCursor(*page.Next)
		resp.NextCursor = &c
		resp.HasMore = true
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *DocumentHandlers) Get(w http.ResponseWriter, r *http.Request) {
	detail, err := h.uc.GetDocument(r.Context(), UserIDFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toDetailResponse(detail))
}

func (h *DocumentHandlers) Download(w http.ResponseWriter, r *http.Request) {
	doc, rc, err := h.uc.OpenFile(r.Context(), UserIDFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer func() { _ = rc.Close() }()
	hd := w.Header()
	hd.Set("Content-Type", doc.ContentType)
	hd.Set("Content-Length", strconv.FormatInt(doc.SizeBytes, 10))
	hd.Set("Content-Disposition", contentDisposition(doc.FileName))
	hd.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func contentDisposition(name string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f, r == '"', r == '\\', r == '/', r == ';':
			return '_'
		}
		return r
	}, name)
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": safe}); v != "" {
		return v
	}
	return "attachment"
}
