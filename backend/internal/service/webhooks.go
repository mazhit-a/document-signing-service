package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

const (
	maxWebhookURLLength = 2048
	webhookSecretBytes  = 32
	webhookSecretPrefix = "whsec_"
)

type WebhookService struct {
	endpoints WebhookEndpointRepository
	clock     Clock
	ids       IDGenerator
}

func NewWebhookService(endpoints WebhookEndpointRepository, clock Clock, ids IDGenerator) *WebhookService {
	return &WebhookService{endpoints: endpoints, clock: clock, ids: ids}
}

func (s *WebhookService) Create(ctx context.Context, ownerID, rawURL string) (domain.WebhookEndpoint, error) {
	if msg := validateWebhookURL(rawURL); msg != "" {
		return domain.WebhookEndpoint{}, domain.NewValidationError(map[string]string{"url": msg})
	}
	raw := make([]byte, webhookSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return domain.WebhookEndpoint{}, fmt.Errorf("generate webhook secret: %w", err)
	}
	e := domain.WebhookEndpoint{
		ID:        s.ids.NewID(),
		OwnerID:   ownerID,
		URL:       rawURL,
		Secret:    webhookSecretPrefix + hex.EncodeToString(raw),
		Active:    true,
		CreatedAt: s.clock.Now().UTC().Truncate(time.Microsecond),
	}
	if err := s.endpoints.Create(ctx, e); err != nil {
		return domain.WebhookEndpoint{}, err
	}
	return e, nil
}

func (s *WebhookService) List(ctx context.Context, ownerID string) ([]domain.WebhookEndpoint, error) {
	return s.endpoints.ListByOwner(ctx, ownerID)
}

func (s *WebhookService) Delete(ctx context.Context, ownerID, id string) error {
	return s.endpoints.Delete(ctx, ownerID, id)
}

func validateWebhookURL(raw string) string {
	if raw == "" {
		return "is required"
	}
	if len(raw) > maxWebhookURLLength {
		return "is too long"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "must be an absolute http or https URL"
	}
	if u.User != nil {
		return "must not contain credentials"
	}
	return ""
}
