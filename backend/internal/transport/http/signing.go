package http

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

const maxUserAgentRunes = 512

type SigningUseCase interface {
	View(ctx context.Context, token string) (service.SigningView, error)
	OpenFile(ctx context.Context, token string) (domain.Document, io.ReadCloser, error)
	Approve(ctx context.Context, token string, meta service.ClientMeta) (service.DecisionResult, error)
	Reject(ctx context.Context, token, reason string) (service.DecisionResult, error)
}

type SigningHandlers struct {
	uc SigningUseCase
}

func NewSigningHandlers(uc SigningUseCase) *SigningHandlers {
	return &SigningHandlers{uc: uc}
}

type signDocumentInfo struct {
	Title     string `json:"title"`
	FileName  string `json:"file_name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Status    string `json:"status"`
}

type signSignerInfo struct {
	FullName string `json:"full_name"`
	Order    int    `json:"order"`
	Total    int    `json:"total"`
	Status   string `json:"status"`
}

type signViewResponse struct {
	Document signDocumentInfo `json:"document"`
	Signer   signSignerInfo   `json:"signer"`
	CanAct   bool             `json:"can_act"`
}

type decisionResponse struct {
	SignerStatus   string `json:"signer_status"`
	DocumentStatus string `json:"document_status"`
	SignedAt       string `json:"signed_at"`
	Seal           string `json:"seal,omitempty"`
}

type rejectRequest struct {
	Reason string `json:"reason"`
}

func (h *SigningHandlers) View(w http.ResponseWriter, r *http.Request) {
	view, err := h.uc.View(r.Context(), r.PathValue("token"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, signViewResponse{
		Document: signDocumentInfo{
			Title: view.Document.Title, FileName: view.Document.FileName, SizeBytes: view.Document.SizeBytes,
			SHA256: view.Document.SHA256, Status: string(view.Document.Status),
		},
		Signer: signSignerInfo{
			FullName: view.Signer.FullName, Order: view.Signer.Order, Total: view.Total, Status: string(view.Signer.Status),
		},
		CanAct: view.CanAct,
	})
}

func (h *SigningHandlers) File(w http.ResponseWriter, r *http.Request) {
	doc, rc, err := h.uc.OpenFile(r.Context(), r.PathValue("token"))
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
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (h *SigningHandlers) Approve(w http.ResponseWriter, r *http.Request) {
	res, err := h.uc.Approve(r.Context(), r.PathValue("token"), clientMeta(r))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	writeDecision(w, res)
}

func (h *SigningHandlers) Reject(w http.ResponseWriter, r *http.Request) {
	var req rejectRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	res, err := h.uc.Reject(r.Context(), r.PathValue("token"), req.Reason)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	writeDecision(w, res)
}

func writeDecision(w http.ResponseWriter, res service.DecisionResult) {
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, decisionResponse{
		SignerStatus: string(res.SignerStatus), DocumentStatus: string(res.DocumentStatus),
		SignedAt: formatTime(res.SignedAt), Seal: res.Seal,
	})
}

func clientMeta(r *http.Request) service.ClientMeta {
	ip := ""
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && net.ParseIP(host) != nil {
		ip = host
	}
	ua := r.UserAgent()
	if utf8.RuneCountInString(ua) > maxUserAgentRunes {
		ua = string([]rune(ua)[:maxUserAgentRunes])
	}
	return service.ClientMeta{IP: ip, UserAgent: ua}
}
