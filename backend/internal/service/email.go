package service

import (
	"context"
	"strings"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/notify/email"
)

type EmailOutbox interface {
	Enqueue(ctx context.Context, msg email.Outgoing) error
}

type OwnerLookup interface {
	GetByID(ctx context.Context, id string) (domain.User, error)
}

type emailer struct {
	outbox    EmailOutbox
	owners    OwnerLookup
	publicURL string
}

func (e *emailer) signingURL(token string) string {
	return e.publicURL + "/sign/" + token
}

func (e *emailer) enabled() bool {
	return e.outbox != nil && e.owners != nil
}

func (e *emailer) signerTurn(ctx context.Context, doc domain.Document, signer domain.Signer, link string, at time.Time) error {
	if !e.enabled() {
		return nil
	}
	owner, err := e.owners.GetByID(ctx, doc.OwnerID)
	if err != nil {
		return err
	}
	return e.outbox.Enqueue(ctx, email.Outgoing{
		DocumentID: doc.ID, Kind: email.KindSignerTurn, Recipient: signer.Email, At: at,
		Payload: email.Payload{
			DocumentTitle: doc.Title, OwnerName: owner.FullName, OwnerEmail: owner.Email,
			SignerName: signer.FullName, SignerEmail: signer.Email,
			Position: signer.Order, Total: len(doc.Signers), Link: link,
		},
	})
}

func (e *emailer) ownerCompleted(ctx context.Context, doc domain.Document, at time.Time) error {
	if !e.enabled() {
		return nil
	}
	owner, err := e.owners.GetByID(ctx, doc.OwnerID)
	if err != nil {
		return err
	}
	return e.outbox.Enqueue(ctx, email.Outgoing{
		DocumentID: doc.ID, Kind: email.KindOwnerCompleted, Recipient: owner.Email, At: at,
		Payload: email.Payload{
			DocumentTitle: doc.Title, OwnerName: owner.FullName, OwnerEmail: owner.Email, Total: len(doc.Signers),
		},
	})
}

func (e *emailer) ownerRejected(ctx context.Context, doc domain.Document, signer domain.Signer, reason string, at time.Time) error {
	if !e.enabled() {
		return nil
	}
	owner, err := e.owners.GetByID(ctx, doc.OwnerID)
	if err != nil {
		return err
	}
	return e.outbox.Enqueue(ctx, email.Outgoing{
		DocumentID: doc.ID, Kind: email.KindOwnerRejected, Recipient: owner.Email, At: at,
		Payload: email.Payload{
			DocumentTitle: doc.Title, OwnerName: owner.FullName, OwnerEmail: owner.Email,
			SignerName: signer.FullName, SignerEmail: signer.Email, Position: signer.Order, Total: len(doc.Signers),
			Reason: reason,
		},
	})
}

func trimBase(u string) string { return strings.TrimRight(u, "/") }
