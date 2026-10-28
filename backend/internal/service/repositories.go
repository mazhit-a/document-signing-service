package service

import (
	"context"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

type UserRepository interface {
	Create(ctx context.Context, u domain.User) (domain.User, error)
	GetByID(ctx context.Context, id string) (domain.User, error)
	GetByEmail(ctx context.Context, email string) (domain.User, error)
}

type DocumentCursor struct {
	CreatedAt time.Time
	ID        string
}

type DocumentListParams struct {
	OwnerID string
	Status  *domain.DocumentStatus
	Limit   int
	After   *DocumentCursor
}

type DocumentPage struct {
	Items []domain.Document
	Next  *DocumentCursor
}

type DocumentRepository interface {
	Create(ctx context.Context, d domain.Document) error
	GetForOwner(ctx context.Context, ownerID, id string) (domain.Document, error)
	GetByID(ctx context.Context, id string) (domain.Document, error)
	GetForUpdate(ctx context.Context, id string) (domain.Document, error)
	List(ctx context.Context, p DocumentListParams) (DocumentPage, error)
	UpdateStatus(ctx context.Context, id string, status domain.DocumentStatus, updatedAt time.Time, completedAt *time.Time) error
}

type SignerRepository interface {
	GetByID(ctx context.Context, id string) (domain.Signer, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (domain.Signer, error)
	ListByDocument(ctx context.Context, documentID string) ([]domain.Signer, error)
	SetTokenHash(ctx context.Context, id, tokenHash string) error
	UpdateStatus(ctx context.Context, id string, status domain.SignerStatus, decidedAt *time.Time, reason string) error
}

type SignatureRepository interface {
	Insert(ctx context.Context, s domain.Signature) error
	ListByDocument(ctx context.Context, documentID string) ([]domain.Signature, error)
	GetBySigner(ctx context.Context, signerID string) (domain.Signature, error)
}

type AuditRepository interface {
	Append(ctx context.Context, documentID string, typ domain.EventType, actor string, data map[string]any, at time.Time) (domain.AuditEvent, error)
	ListByDocument(ctx context.Context, documentID string) ([]domain.AuditEvent, error)
}

type WebhookEndpointRepository interface {
	Create(ctx context.Context, e domain.WebhookEndpoint) error
	ListByOwner(ctx context.Context, ownerID string) ([]domain.WebhookEndpoint, error)
	Delete(ctx context.Context, ownerID, id string) error
}

type WebhookOutbox interface {
	Enqueue(ctx context.Context, event domain.AuditEvent) error
}
