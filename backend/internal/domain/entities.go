package domain

import "time"

type DocumentStatus string

const (
	DocumentDraft      DocumentStatus = "draft"
	DocumentInProgress DocumentStatus = "in_progress"
	DocumentCompleted  DocumentStatus = "completed"
	DocumentRejected   DocumentStatus = "rejected"
	DocumentCancelled  DocumentStatus = "cancelled"
)

func (s DocumentStatus) IsTerminal() bool {
	return s == DocumentCompleted || s == DocumentRejected || s == DocumentCancelled
}

func (s DocumentStatus) Valid() bool {
	switch s {
	case DocumentDraft, DocumentInProgress, DocumentCompleted, DocumentRejected, DocumentCancelled:
		return true
	}
	return false
}

type SignerStatus string

const (
	SignerWaiting  SignerStatus = "waiting"
	SignerPending  SignerStatus = "pending"
	SignerSigned   SignerStatus = "signed"
	SignerRejected SignerStatus = "rejected"
)

func (s SignerStatus) Valid() bool {
	switch s {
	case SignerWaiting, SignerPending, SignerSigned, SignerRejected:
		return true
	}
	return false
}

type EventType string

const (
	EventDocumentCreated   EventType = "document.created"
	EventDocumentSent      EventType = "document.sent"
	EventSignerApproved    EventType = "signer.approved"
	EventSignerRejected    EventType = "signer.rejected"
	EventDocumentCompleted EventType = "document.completed"
	EventDocumentRejected  EventType = "document.rejected"
	EventDocumentCancelled EventType = "document.cancelled"
	EventDocumentVerified  EventType = "document.verified"
)

func (t EventType) IsWebhookEvent() bool {
	switch t {
	case EventDocumentCompleted, EventDocumentRejected, EventSignerApproved:
		return true
	}
	return false
}

type WebhookEndpoint struct {
	ID        string
	OwnerID   string
	URL       string
	Secret    string
	Active    bool
	CreatedAt time.Time
}

type User struct {
	ID           string
	Email        string
	PasswordHash string
	FullName     string
	CreatedAt    time.Time
}

type Signer struct {
	ID           string
	DocumentID   string
	Order        int
	FullName     string
	Email        string
	Status       SignerStatus
	TokenHash    string
	DecidedAt    *time.Time
	RejectReason string
}

type Document struct {
	ID          string
	OwnerID     string
	Title       string
	FileName    string
	ContentType string
	SizeBytes   int64
	StorageKey  string
	SHA256      string
	Status      DocumentStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
	Signers     []Signer
}

type Signature struct {
	ID             string
	SignerID       string
	DocumentID     string
	DocumentSHA256 string
	Payload        string
	Seal           string
	SignedAt       time.Time
	IP             string
	UserAgent      string
}

type AuditEvent struct {
	ID         int64
	DocumentID string
	Type       EventType
	Actor      string
	Data       map[string]any
	PrevHash   string
	Hash       string
	CreatedAt  time.Time
}
