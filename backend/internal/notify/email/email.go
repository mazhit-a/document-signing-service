package email

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"time"
)

type Kind string

const (
	KindSignerTurn     Kind = "signer_turn"
	KindOwnerCompleted Kind = "owner_completed"
	KindOwnerRejected  Kind = "owner_rejected"
)

func (k Kind) Valid() bool {
	switch k {
	case KindSignerTurn, KindOwnerCompleted, KindOwnerRejected:
		return true
	}
	return false
}

const (
	MaxAttempts    = 6
	jitterFraction = 0.2
)

type Status string

const (
	StatusPending Status = "pending"
	StatusSent    Status = "sent"
	StatusFailed  Status = "failed"
)

type Payload struct {
	DocumentTitle string `json:"document_title"`
	OwnerName     string `json:"owner_name"`
	OwnerEmail    string `json:"owner_email"`
	SignerName    string `json:"signer_name,omitempty"`
	SignerEmail   string `json:"signer_email,omitempty"`
	Position      int    `json:"position,omitempty"`
	Total         int    `json:"total,omitempty"`
	Link          string `json:"link,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type Outgoing struct {
	DocumentID string
	Kind       Kind
	Recipient  string
	Payload    Payload
	At         time.Time
}

type Item struct {
	ID         string
	Kind       Kind
	Recipient  string
	Attempts   int
	Ciphertext []byte
}

type Outcome struct {
	Status        Status
	NextAttemptAt time.Time
	Error         string
}

type Store interface {
	ProcessNext(ctx context.Context, now time.Time, handle func(ctx context.Context, it Item) (Outcome, error)) (bool, error)
}

type Message struct {
	To      string
	Subject string
	Body    string
}

type Sender interface {
	Send(ctx context.Context, msg Message) error
}

var backoffSchedule = []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

func Backoff(failedAttempts int) time.Duration {
	if failedAttempts < 1 {
		failedAttempts = 1
	}
	if failedAttempts > len(backoffSchedule) {
		failedAttempts = len(backoffSchedule)
	}
	return backoffSchedule[failedAttempts-1]
}

func RandomJitter(d time.Duration) time.Duration {
	delta := (rand.Float64()*2 - 1) * jitterFraction * float64(d)
	return d + time.Duration(delta)
}

func DecodePayload(plain []byte) (Payload, error) {
	var p Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		return Payload{}, errors.New("email payload is malformed")
	}
	return p, nil
}
