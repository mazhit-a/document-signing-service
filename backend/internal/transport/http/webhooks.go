package http

import (
	"context"
	"net/http"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

type WebhookUseCase interface {
	Create(ctx context.Context, ownerID, rawURL string) (domain.WebhookEndpoint, error)
	List(ctx context.Context, ownerID string) ([]domain.WebhookEndpoint, error)
	Delete(ctx context.Context, ownerID, id string) error
}

type WebhookHandlers struct {
	uc WebhookUseCase
}

func NewWebhookHandlers(uc WebhookUseCase) *WebhookHandlers {
	return &WebhookHandlers{uc: uc}
}

type createWebhookRequest struct {
	URL string `json:"url"`
}

type webhookResponse struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}

type createdWebhookResponse struct {
	webhookResponse
	Secret string `json:"secret"`
	Note   string `json:"note"`
}

type webhookListResponse struct {
	Items []webhookResponse `json:"items"`
}

func toWebhookResponse(e domain.WebhookEndpoint) webhookResponse {
	return webhookResponse{ID: e.ID, URL: e.URL, Active: e.Active, CreatedAt: formatTime(e.CreatedAt)}
}

func (h *WebhookHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req createWebhookRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	e, err := h.uc.Create(r.Context(), UserIDFrom(r.Context()), req.URL)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusCreated, createdWebhookResponse{webhookResponse: toWebhookResponse(e), Secret: e.Secret, Note: "shown once"})
}

func (h *WebhookHandlers) List(w http.ResponseWriter, r *http.Request) {
	endpoints, err := h.uc.List(r.Context(), UserIDFrom(r.Context()))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	resp := webhookListResponse{Items: make([]webhookResponse, len(endpoints))}
	for i, e := range endpoints {
		resp.Items[i] = toWebhookResponse(e)
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *WebhookHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.uc.Delete(r.Context(), UserIDFrom(r.Context()), r.PathValue("id")); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
