package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/storage"
)

type ClientMeta struct {
	IP        string
	UserAgent string
}

type SigningView struct {
	Document domain.Document
	Signer   domain.Signer
	Total    int
	CanAct   bool
}

type DecisionResult struct {
	SignerStatus   domain.SignerStatus
	DocumentStatus domain.DocumentStatus
	SignedAt       time.Time
	Seal           string
}

type SigningService struct {
	docs       DocumentRepository
	signers    SignerRepository
	signatures SignatureRepository
	audit      AuditRepository
	files      storage.FileStorage
	tx         TxManager
	clock      Clock
	ids        IDGenerator
	notifier   notify.Notifier
	sealSecret []byte
	outbox     WebhookOutbox
	mail       emailer
}

func NewSigningService(docs DocumentRepository, signers SignerRepository, signatures SignatureRepository, audit AuditRepository, files storage.FileStorage, tx TxManager, clock Clock, ids IDGenerator, notifier notify.Notifier, sealSecret []byte) *SigningService {
	return &SigningService{
		docs: docs, signers: signers, signatures: signatures, audit: audit, files: files,
		tx: tx, clock: clock, ids: ids, notifier: notifier, sealSecret: sealSecret,
	}
}

func (s *SigningService) WithOutbox(outbox WebhookOutbox) *SigningService {
	s.outbox = outbox
	return s
}

func (s *SigningService) WithEmail(outbox EmailOutbox, owners OwnerLookup, publicURL string) *SigningService {
	s.mail = emailer{outbox: outbox, owners: owners, publicURL: trimBase(publicURL)}
	return s
}

func (s *SigningService) record(ctx context.Context, documentID string, typ domain.EventType, actor string, data map[string]any, at time.Time) error {
	ev, err := s.audit.Append(ctx, documentID, typ, actor, data, at)
	if err != nil {
		return err
	}
	if s.outbox == nil || !typ.IsWebhookEvent() {
		return nil
	}
	return s.outbox.Enqueue(ctx, ev)
}

func (s *SigningService) resolve(ctx context.Context, token string) (domain.Document, domain.Signer, error) {
	hash := integrity.HashToken(token)
	signer, err := s.signers.GetByTokenHash(ctx, hash)
	if err != nil {
		return domain.Document{}, domain.Signer{}, err
	}
	doc, err := s.docs.GetByID(ctx, signer.DocumentID)
	if err != nil {
		return domain.Document{}, domain.Signer{}, err
	}
	return doc, signer, nil
}

func (s *SigningService) lockByToken(ctx context.Context, token string) (domain.Document, domain.Signer, error) {
	hash := integrity.HashToken(token)
	signer, err := s.signers.GetByTokenHash(ctx, hash)
	if err != nil {
		return domain.Document{}, domain.Signer{}, err
	}
	doc, err := s.docs.GetForUpdate(ctx, signer.DocumentID)
	if err != nil {
		return domain.Document{}, domain.Signer{}, err
	}
	current, err := doc.SignerByID(signer.ID)
	if err != nil || current.TokenHash != hash {
		return domain.Document{}, domain.Signer{}, domain.ErrNotFound
	}
	return doc, *current, nil
}

func (s *SigningService) View(ctx context.Context, token string) (SigningView, error) {
	doc, signer, err := s.resolve(ctx, token)
	if err != nil {
		return SigningView{}, err
	}
	current, err := doc.SignerByID(signer.ID)
	if err != nil {
		return SigningView{}, err
	}
	return SigningView{
		Document: doc,
		Signer:   *current,
		Total:    len(doc.Signers),
		CanAct:   doc.Status == domain.DocumentInProgress && current.Status == domain.SignerPending,
	}, nil
}

func (s *SigningService) OpenFile(ctx context.Context, token string) (domain.Document, io.ReadCloser, error) {
	doc, _, err := s.resolve(ctx, token)
	if err != nil {
		return domain.Document{}, nil, err
	}
	rc, err := s.files.Open(ctx, doc.StorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return domain.Document{}, nil, fmt.Errorf("stored file missing for document %s: %w", doc.ID, err)
		}
		return domain.Document{}, nil, err
	}
	return doc, rc, nil
}

func (s *SigningService) Approve(ctx context.Context, token string, meta ClientMeta) (DecisionResult, error) {
	var result DecisionResult
	var next *notify.SigningRequest

	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		doc, signer, err := s.lockByToken(ctx, token)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC().Truncate(time.Microsecond)
		res, err := doc.Approve(signer.ID, now)
		if err != nil {
			return err
		}
		if res.AlreadySigned {
			sig, err := s.signatures.GetBySigner(ctx, signer.ID)
			if err != nil {
				return err
			}
			result = DecisionResult{SignerStatus: domain.SignerSigned, DocumentStatus: doc.Status, SignedAt: sig.SignedAt.UTC(), Seal: sig.Seal}
			return nil
		}

		payload := integrity.BuildPayload(doc.ID, doc.SHA256, signer.ID, signer.Order, now)
		seal := integrity.Seal(s.sealSecret, payload)
		if err := s.signatures.Insert(ctx, domain.Signature{
			ID: s.ids.NewID(), SignerID: signer.ID, DocumentID: doc.ID, DocumentSHA256: doc.SHA256,
			Payload: payload, Seal: seal, SignedAt: now, IP: meta.IP, UserAgent: meta.UserAgent,
		}); err != nil {
			return err
		}
		if err := s.signers.UpdateStatus(ctx, signer.ID, domain.SignerSigned, &now, ""); err != nil {
			return err
		}
		if err := s.record(ctx, doc.ID, domain.EventSignerApproved, signer.ID, map[string]any{
			"signer_id": signer.ID, "order": signer.Order,
		}, now); err != nil {
			return err
		}

		if res.DocumentDone {
			if err := s.docs.UpdateStatus(ctx, doc.ID, doc.Status, now, doc.CompletedAt); err != nil {
				return err
			}
			if err := s.record(ctx, doc.ID, domain.EventDocumentCompleted, signer.ID, map[string]any{
				"signer_count": len(doc.Signers),
			}, now); err != nil {
				return err
			}
			if err := s.mail.ownerCompleted(ctx, doc, now); err != nil {
				return err
			}
		} else {
			if err := s.signers.UpdateStatus(ctx, res.NextSignerID, domain.SignerPending, nil, ""); err != nil {
				return err
			}
			if err := s.docs.UpdateStatus(ctx, doc.ID, doc.Status, now, nil); err != nil {
				return err
			}
			following, err := doc.SignerByID(res.NextSignerID)
			if err != nil {
				return err
			}
			nextToken, err := integrity.NewToken()
			if err != nil {
				return err
			}
			if err := s.signers.SetTokenHash(ctx, following.ID, integrity.HashToken(nextToken)); err != nil {
				return err
			}
			link := s.mail.signingURL(nextToken)
			if err := s.mail.signerTurn(ctx, doc, *following, link, now); err != nil {
				return err
			}
			next = &notify.SigningRequest{
				DocumentID: doc.ID, DocumentTitle: doc.Title,
				SignerID: following.ID, SignerName: following.FullName, SignerEmail: following.Email,
				Order: following.Order, URL: link,
			}
		}

		result = DecisionResult{SignerStatus: domain.SignerSigned, DocumentStatus: doc.Status, SignedAt: now, Seal: seal}
		return nil
	})
	if err != nil {
		return DecisionResult{}, err
	}
	if next != nil {
		_ = s.notifier.SigningRequested(context.WithoutCancel(ctx), *next)
	}
	return result, nil
}

func (s *SigningService) Reject(ctx context.Context, token, reason string) (DecisionResult, error) {
	var result DecisionResult

	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		doc, signer, err := s.lockByToken(ctx, token)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC().Truncate(time.Microsecond)
		if err := doc.Reject(signer.ID, reason, now); err != nil {
			return err
		}
		stored, err := doc.SignerByID(signer.ID)
		if err != nil {
			return err
		}
		if err := s.signers.UpdateStatus(ctx, signer.ID, domain.SignerRejected, &now, stored.RejectReason); err != nil {
			return err
		}
		if err := s.docs.UpdateStatus(ctx, doc.ID, doc.Status, now, nil); err != nil {
			return err
		}
		if _, err := s.audit.Append(ctx, doc.ID, domain.EventSignerRejected, signer.ID, map[string]any{
			"signer_id": signer.ID, "order": signer.Order, "reason": stored.RejectReason,
		}, now); err != nil {
			return err
		}
		if err := s.record(ctx, doc.ID, domain.EventDocumentRejected, signer.ID, map[string]any{
			"signer_id": signer.ID,
		}, now); err != nil {
			return err
		}
		if err := s.mail.ownerRejected(ctx, doc, signer, stored.RejectReason, now); err != nil {
			return err
		}
		result = DecisionResult{SignerStatus: domain.SignerRejected, DocumentStatus: doc.Status, SignedAt: now}
		return nil
	})
	if err != nil {
		return DecisionResult{}, err
	}
	return result, nil
}
