package http

import (
	"context"
	"net/http"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

type LifecycleUseCase interface {
	SendDocument(ctx context.Context, ownerID, id string) (service.SendResult, error)
	CancelDocument(ctx context.Context, ownerID, id string) (domain.Document, error)
	ListEvents(ctx context.Context, ownerID, id string) ([]domain.AuditEvent, error)
}

type LifecycleHandlers struct {
	uc LifecycleUseCase
}

func NewLifecycleHandlers(uc LifecycleUseCase) *LifecycleHandlers {
	return &LifecycleHandlers{uc: uc}
}

const sendNote = "Only the first signer's link is returned. Each following signer receives a link by email when it becomes their turn."

type signingLinkResponse struct {
	SignerID string `json:"signer_id"`
	Order    int    `json:"order"`
	URL      string `json:"url"`
	Note     string `json:"note"`
}

type sendResponse struct {
	Status       string                `json:"status"`
	Note         string                `json:"note"`
	SigningLinks []signingLinkResponse `json:"signing_links"`
}

type cancelResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

type eventResponse struct {
	ID        int64          `json:"id"`
	Type      string         `json:"type"`
	Actor     string         `json:"actor"`
	Data      map[string]any `json:"data"`
	PrevHash  string         `json:"prev_hash"`
	Hash      string         `json:"hash"`
	CreatedAt string         `json:"created_at"`
}

type eventListResponse struct {
	Items []eventResponse `json:"items"`
}

func (h *LifecycleHandlers) Send(w http.ResponseWriter, r *http.Request) {
	res, err := h.uc.SendDocument(r.Context(), UserIDFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	resp := sendResponse{Status: string(res.Document.Status), Note: sendNote, SigningLinks: make([]signingLinkResponse, len(res.Links))}
	for i, l := range res.Links {
		resp.SigningLinks[i] = signingLinkResponse{SignerID: l.SignerID, Order: l.Order, URL: l.URL, Note: "shown once"}
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, resp)
}

func (h *LifecycleHandlers) Cancel(w http.ResponseWriter, r *http.Request) {
	doc, err := h.uc.CancelDocument(r.Context(), UserIDFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, cancelResponse{ID: doc.ID, Status: string(doc.Status), UpdatedAt: formatTime(doc.UpdatedAt)})
}

func (h *LifecycleHandlers) Events(w http.ResponseWriter, r *http.Request) {
	events, err := h.uc.ListEvents(r.Context(), UserIDFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	resp := eventListResponse{Items: make([]eventResponse, len(events))}
	for i, e := range events {
		data := e.Data
		if data == nil {
			data = map[string]any{}
		}
		resp.Items[i] = eventResponse{
			ID: e.ID, Type: string(e.Type), Actor: e.Actor, Data: data,
			PrevHash: e.PrevHash, Hash: e.Hash, CreatedAt: formatTime(e.CreatedAt),
		}
	}
	WriteJSON(w, http.StatusOK, resp)
}
