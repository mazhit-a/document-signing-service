package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/notify"
)

type SigningLink struct {
	SignerID string
	Order    int
	URL      string
}

type SendResult struct {
	Document domain.Document
	Links    []SigningLink
}

type LifecycleService struct {
	docs      DocumentRepository
	signers   SignerRepository
	audit     AuditRepository
	tx        TxManager
	clock     Clock
	notifier  notify.Notifier
	publicURL string
}

func NewLifecycleService(docs DocumentRepository, signers SignerRepository, audit AuditRepository, tx TxManager, clock Clock, notifier notify.Notifier, publicURL string) *LifecycleService {
	return &LifecycleService{
		docs: docs, signers: signers, audit: audit, tx: tx, clock: clock,
		notifier: notifier, publicURL: strings.TrimRight(publicURL, "/"),
	}
}

func (s *LifecycleService) lockOwned(ctx context.Context, ownerID, id string) (domain.Document, error) {
	doc, err := s.docs.GetForUpdate(ctx, id)
	if err != nil {
		return domain.Document{}, err
	}
	if doc.OwnerID != ownerID {
		return domain.Document{}, domain.ErrNotFound
	}
	return doc, nil
}

func (s *LifecycleService) SendDocument(ctx context.Context, ownerID, id string) (SendResult, error) {
	var result SendResult
	var first notify.SigningRequest

	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		doc, err := s.lockOwned(ctx, ownerID, id)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC().Truncate(time.Microsecond)
		if err := doc.Send(now); err != nil {
			return err
		}

		links := make([]SigningLink, len(doc.Signers))
		for i, signer := range doc.Signers {
			token, err := integrity.NewToken()
			if err != nil {
				return err
			}
			if err := s.signers.SetTokenHash(ctx, signer.ID, integrity.HashToken(token)); err != nil {
				return err
			}
			links[i] = SigningLink{SignerID: signer.ID, Order: signer.Order, URL: s.publicURL + "/sign/" + token}
		}
		pending := doc.Signers[0]
		if err := s.signers.UpdateStatus(ctx, pending.ID, domain.SignerPending, nil, ""); err != nil {
			return err
		}
		if err := s.docs.UpdateStatus(ctx, doc.ID, doc.Status, now, nil); err != nil {
			return err
		}
		if _, err := s.audit.Append(ctx, doc.ID, domain.EventDocumentSent, ownerID, map[string]any{
			"signer_count": len(doc.Signers),
		}, now); err != nil {
			return err
		}

		result = SendResult{Document: doc, Links: links}
		first = notify.SigningRequest{
			DocumentID: doc.ID, DocumentTitle: doc.Title,
			SignerID: pending.ID, SignerName: pending.FullName, SignerEmail: pending.Email,
			Order: pending.Order, URL: links[0].URL,
		}
		return nil
	})
	if err != nil {
		return SendResult{}, err
	}

	_ = s.notifier.SigningRequested(context.WithoutCancel(ctx), first)
	return result, nil
}

func (s *LifecycleService) CancelDocument(ctx context.Context, ownerID, id string) (domain.Document, error) {
	var result domain.Document

	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		doc, err := s.lockOwned(ctx, ownerID, id)
		if err != nil {
			return err
		}
		previous := doc.Status
		wasPending := map[string]bool{}
		for _, signer := range doc.Signers {
			wasPending[signer.ID] = signer.Status == domain.SignerPending
		}
		now := s.clock.Now().UTC().Truncate(time.Microsecond)
		if err := doc.Cancel(now); err != nil {
			return err
		}

		for _, signer := range doc.Signers {
			if err := s.signers.SetTokenHash(ctx, signer.ID, ""); err != nil {
				return err
			}
			if wasPending[signer.ID] {
				if err := s.signers.UpdateStatus(ctx, signer.ID, signer.Status, nil, ""); err != nil {
					return err
				}
			}
		}
		if err := s.docs.UpdateStatus(ctx, doc.ID, doc.Status, now, nil); err != nil {
			return err
		}
		if _, err := s.audit.Append(ctx, doc.ID, domain.EventDocumentCancelled, ownerID, map[string]any{
			"previous_status": string(previous),
		}, now); err != nil {
			return err
		}
		result = doc
		return nil
	})
	if err != nil {
		return domain.Document{}, err
	}
	return result, nil
}

func (s *LifecycleService) ListEvents(ctx context.Context, ownerID, id string) ([]domain.AuditEvent, error) {
	doc, err := s.docs.GetForOwner(ctx, ownerID, id)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return s.audit.ListByDocument(ctx, doc.ID)
}
