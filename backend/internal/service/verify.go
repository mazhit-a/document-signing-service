package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/storage"
)

type FileCheck struct {
	OK             bool
	ExpectedSHA256 string
	ActualSHA256   string
}

type SignatureCheck struct {
	SignerID            string
	Order               int
	SealOK              bool
	HashMatchesDocument bool
}

type ChainCheck struct {
	OK                 bool
	Events             int
	FirstBrokenEventID *int64
}

type VerifyReport struct {
	Valid      bool
	CheckedAt  time.Time
	File       FileCheck
	Signatures []SignatureCheck
	AuditChain ChainCheck
}

type VerifyService struct {
	docs       DocumentRepository
	signatures SignatureRepository
	audit      AuditRepository
	files      storage.FileStorage
	tx         TxManager
	clock      Clock
	sealSecret []byte
}

func NewVerifyService(docs DocumentRepository, signatures SignatureRepository, audit AuditRepository, files storage.FileStorage, tx TxManager, clock Clock, sealSecret []byte) *VerifyService {
	return &VerifyService{docs: docs, signatures: signatures, audit: audit, files: files, tx: tx, clock: clock, sealSecret: sealSecret}
}

func (s *VerifyService) Verify(ctx context.Context, ownerID, id string) (VerifyReport, error) {
	var report VerifyReport

	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		doc, err := s.docs.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if doc.OwnerID != ownerID {
			return domain.ErrNotFound
		}
		now := s.clock.Now().UTC().Truncate(time.Microsecond)

		file, err := s.checkFile(ctx, doc)
		if err != nil {
			return err
		}
		sigs, err := s.checkSignatures(ctx, doc)
		if err != nil {
			return err
		}
		chain, err := s.checkChain(ctx, doc.ID)
		if err != nil {
			return err
		}

		valid := file.OK && chain.OK
		for _, sc := range sigs {
			valid = valid && sc.SealOK && sc.HashMatchesDocument
		}
		report = VerifyReport{Valid: valid, CheckedAt: now, File: file, Signatures: sigs, AuditChain: chain}

		result := "invalid"
		if valid {
			result = "valid"
		}
		_, err = s.audit.Append(ctx, doc.ID, domain.EventDocumentVerified, ownerID, map[string]any{"result": result}, now)
		return err
	})
	if err != nil {
		return VerifyReport{}, err
	}
	return report, nil
}

func (s *VerifyService) checkFile(ctx context.Context, doc domain.Document) (FileCheck, error) {
	check := FileCheck{ExpectedSHA256: doc.SHA256}
	rc, err := s.files.Open(ctx, doc.StorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrInvalidKey) {
			return check, nil
		}
		return FileCheck{}, fmt.Errorf("open stored file: %w", err)
	}
	defer rc.Close()
	sum, _, err := integrity.SHA256Reader(rc)
	if err != nil {
		return FileCheck{}, fmt.Errorf("hash stored file: %w", err)
	}
	check.ActualSHA256 = sum
	check.OK = sum == doc.SHA256
	return check, nil
}

func (s *VerifyService) checkSignatures(ctx context.Context, doc domain.Document) ([]SignatureCheck, error) {
	sigs, err := s.signatures.ListByDocument(ctx, doc.ID)
	if err != nil {
		return nil, err
	}
	out := make([]SignatureCheck, 0, len(sigs))
	for _, sig := range sigs {
		check := SignatureCheck{
			SignerID:            sig.SignerID,
			SealOK:              integrity.VerifySeal(s.sealSecret, sig.Payload, sig.Seal),
			HashMatchesDocument: sig.DocumentSHA256 == doc.SHA256,
		}
		if signer, err := doc.SignerByID(sig.SignerID); err == nil {
			check.Order = signer.Order
		}
		out = append(out, check)
	}
	return out, nil
}

func (s *VerifyService) checkChain(ctx context.Context, documentID string) (ChainCheck, error) {
	events, err := s.audit.ListByDocument(ctx, documentID)
	if err != nil {
		return ChainCheck{}, err
	}
	chain := make([]integrity.Event, len(events))
	for i, e := range events {
		chain[i] = integrity.Event{ID: e.ID, Type: string(e.Type), Actor: e.Actor, Data: e.Data, CreatedAt: e.CreatedAt, PrevHash: e.PrevHash, Hash: e.Hash}
	}
	rep := integrity.VerifyChain(chain)
	return ChainCheck{OK: rep.OK, Events: rep.Events, FirstBrokenEventID: rep.FirstBrokenEventID}, nil
}
