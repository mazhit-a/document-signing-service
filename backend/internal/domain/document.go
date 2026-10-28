package domain

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MinSigners      = 1
	MaxSigners      = 10
	MaxTitleLength  = 200
	MaxReasonLength = 500
)

type SignerInput struct {
	FullName string
	Email    string
}

func ValidateTitle(title string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(title))
	if n < 1 || n > MaxTitleLength {
		return NewValidationError(map[string]string{"title": "must be 1-200 characters"})
	}
	return nil
}

func ValidateSignerInputs(inputs []SignerInput) error {
	if len(inputs) < MinSigners {
		return NewValidationError(map[string]string{"signers": "at least one signer is required"})
	}
	if len(inputs) > MaxSigners {
		return NewValidationError(map[string]string{"signers": "at most 10 signers are allowed"})
	}
	fields := map[string]string{}
	seen := make(map[string]int, len(inputs))
	for i, in := range inputs {
		key := fmt.Sprintf("signers[%d]", i)
		if strings.TrimSpace(in.FullName) == "" {
			fields[key+".full_name"] = "required"
		}
		addr, err := mail.ParseAddress(in.Email)
		if err != nil || addr.Address != in.Email {
			fields[key+".email"] = "invalid email"
			continue
		}
		normalized := strings.ToLower(in.Email)
		if _, dup := seen[normalized]; dup {
			fields[key+".email"] = "duplicate email"
			continue
		}
		seen[normalized] = i
	}
	if len(fields) > 0 {
		return NewValidationError(fields)
	}
	return nil
}

func NewSigners(documentID string, inputs []SignerInput, newID func() string) ([]Signer, error) {
	if err := ValidateSignerInputs(inputs); err != nil {
		return nil, err
	}
	signers := make([]Signer, len(inputs))
	for i, in := range inputs {
		signers[i] = Signer{
			ID:         newID(),
			DocumentID: documentID,
			Order:      i + 1,
			FullName:   strings.TrimSpace(in.FullName),
			Email:      in.Email,
			Status:     SignerWaiting,
		}
	}
	return signers, nil
}

func (d *Document) CheckInvariants() error {
	n := len(d.Signers)
	if n < MinSigners || n > MaxSigners {
		return fmt.Errorf("%w: signer count %d out of bounds", ErrInvalidState, n)
	}
	pending := 0
	for i, s := range d.Signers {
		if s.Order != i+1 {
			return fmt.Errorf("%w: signer order must be contiguous from 1", ErrInvalidState)
		}
		if s.Status == SignerPending {
			pending++
		}
	}
	if pending > 1 {
		return fmt.Errorf("%w: more than one pending signer", ErrInvalidState)
	}
	if d.Status != DocumentInProgress && pending > 0 {
		return fmt.Errorf("%w: pending signer in %s document", ErrInvalidState, d.Status)
	}
	if d.Status == DocumentInProgress && pending != 1 {
		return fmt.Errorf("%w: in_progress document needs exactly one pending signer", ErrInvalidState)
	}
	return nil
}

func (d *Document) SignerByID(id string) (*Signer, error) {
	for i := range d.Signers {
		if d.Signers[i].ID == id {
			return &d.Signers[i], nil
		}
	}
	return nil, ErrNotFound
}

func (d *Document) Send(now time.Time) error {
	if d.Status.IsTerminal() {
		return ErrDocumentClosed
	}
	if d.Status != DocumentDraft {
		return ErrInvalidState
	}
	if err := d.CheckInvariants(); err != nil {
		return err
	}
	d.Signers[0].Status = SignerPending
	d.Status = DocumentInProgress
	d.UpdatedAt = now
	return nil
}

func (d *Document) Cancel(now time.Time) error {
	if d.Status.IsTerminal() {
		return ErrDocumentClosed
	}
	for i := range d.Signers {
		if d.Signers[i].Status == SignerPending {
			d.Signers[i].Status = SignerWaiting
		}
	}
	d.Status = DocumentCancelled
	d.UpdatedAt = now
	return nil
}

type ApproveResult struct {
	AlreadySigned    bool
	DocumentDone     bool
	NextSignerID     string
	ApprovedSignerID string
}

func (d *Document) Approve(signerID string, now time.Time) (ApproveResult, error) {
	s, err := d.SignerByID(signerID)
	if err != nil {
		return ApproveResult{}, err
	}
	if s.Status == SignerSigned {
		return ApproveResult{AlreadySigned: true, ApprovedSignerID: s.ID}, nil
	}
	if err := d.checkCanAct(s); err != nil {
		return ApproveResult{}, err
	}
	s.Status = SignerSigned
	s.DecidedAt = &now
	res := ApproveResult{ApprovedSignerID: s.ID}
	if s.Order < len(d.Signers) {
		next := &d.Signers[s.Order]
		next.Status = SignerPending
		res.NextSignerID = next.ID
	} else {
		d.Status = DocumentCompleted
		d.CompletedAt = &now
		res.DocumentDone = true
	}
	d.UpdatedAt = now
	return res, nil
}

func (d *Document) Reject(signerID, reason string, now time.Time) error {
	s, err := d.SignerByID(signerID)
	if err != nil {
		return err
	}
	if s.Status == SignerSigned {
		return ErrInvalidState
	}
	if err := d.checkCanAct(s); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 1 || n > MaxReasonLength {
		return NewValidationError(map[string]string{"reason": "must be 1-500 characters"})
	}
	s.Status = SignerRejected
	s.DecidedAt = &now
	s.RejectReason = reason
	d.Status = DocumentRejected
	d.UpdatedAt = now
	return nil
}

func (d *Document) checkCanAct(s *Signer) error {
	if d.Status.IsTerminal() || s.Status == SignerRejected {
		return ErrDocumentClosed
	}
	if d.Status != DocumentInProgress {
		return ErrInvalidState
	}
	if s.Status != SignerPending {
		return ErrNotYourTurn
	}
	return nil
}
