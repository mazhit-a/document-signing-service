package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 28, 5, 0, 0, 0, time.UTC)

func newDoc(t *testing.T, n int, status DocumentStatus) *Document {
	t.Helper()
	inputs := make([]SignerInput, n)
	for i := range inputs {
		inputs[i] = SignerInput{FullName: fmt.Sprintf("Signer %d", i+1), Email: fmt.Sprintf("s%d@x.kz", i+1)}
	}
	c := 0
	signers, err := NewSigners("doc-1", inputs, func() string { c++; return fmt.Sprintf("sg-%d", c) })
	if err != nil {
		t.Fatal(err)
	}
	d := &Document{ID: "doc-1", Title: "T", Status: DocumentDraft, Signers: signers}
	if status != DocumentDraft {
		if err := d.Send(t0); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func signerStatuses(d *Document) []SignerStatus {
	out := make([]SignerStatus, len(d.Signers))
	for i, s := range d.Signers {
		out[i] = s.Status
	}
	return out
}

func TestSend(t *testing.T) {
	tests := []struct {
		name    string
		status  DocumentStatus
		wantErr error
	}{
		{"draft", DocumentDraft, nil},
		{"in_progress", DocumentInProgress, ErrInvalidState},
		{"completed", DocumentCompleted, ErrDocumentClosed},
		{"rejected", DocumentRejected, ErrDocumentClosed},
		{"cancelled", DocumentCancelled, ErrDocumentClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newDoc(t, 3, DocumentDraft)
			d.Status = tc.status
			err := d.Send(t0)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if d.Status != DocumentInProgress {
					t.Fatalf("status %s", d.Status)
				}
				want := []SignerStatus{SignerPending, SignerWaiting, SignerWaiting}
				if got := signerStatuses(d); fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("signers %v", got)
				}
				if err := d.CheckInvariants(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCancel(t *testing.T) {
	tests := []struct {
		name    string
		status  DocumentStatus
		wantErr error
	}{
		{"draft", DocumentDraft, nil},
		{"in_progress", DocumentInProgress, nil},
		{"completed", DocumentCompleted, ErrDocumentClosed},
		{"rejected", DocumentRejected, ErrDocumentClosed},
		{"cancelled", DocumentCancelled, ErrDocumentClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newDoc(t, 2, DocumentDraft)
			if tc.status == DocumentInProgress {
				if err := d.Send(t0); err != nil {
					t.Fatal(err)
				}
			} else {
				d.Status = tc.status
			}
			err := d.Cancel(t0)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if d.Status != DocumentCancelled {
					t.Fatalf("status %s", d.Status)
				}
				for _, s := range d.Signers {
					if s.Status == SignerPending {
						t.Fatal("pending signer left after cancel")
					}
				}
			}
		})
	}
}

func TestApproveSequence(t *testing.T) {
	d := newDoc(t, 3, DocumentInProgress)
	for i := 0; i < 3; i++ {
		res, err := d.Approve(d.Signers[i].ID, t0.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatalf("approve %d: %v", i, err)
		}
		last := i == 2
		if res.DocumentDone != last {
			t.Fatalf("approve %d: done=%v", i, res.DocumentDone)
		}
		if !last {
			if res.NextSignerID != d.Signers[i+1].ID || d.Signers[i+1].Status != SignerPending {
				t.Fatalf("approve %d: next not promoted", i)
			}
			if err := d.CheckInvariants(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if d.Status != DocumentCompleted || d.CompletedAt == nil {
		t.Fatalf("status %s completed_at %v", d.Status, d.CompletedAt)
	}
}

func TestApproveGuards(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) (*Document, string)
		wantErr error
	}{
		{"unknown signer", func(t *testing.T) (*Document, string) { return newDoc(t, 2, DocumentInProgress), "nope" }, ErrNotFound},
		{"waiting signer", func(t *testing.T) (*Document, string) {
			d := newDoc(t, 2, DocumentInProgress)
			return d, d.Signers[1].ID
		}, ErrNotYourTurn},
		{"draft document", func(t *testing.T) (*Document, string) {
			d := newDoc(t, 2, DocumentDraft)
			return d, d.Signers[0].ID
		}, ErrInvalidState},
		{"cancelled document", func(t *testing.T) (*Document, string) {
			d := newDoc(t, 2, DocumentInProgress)
			id := d.Signers[0].ID
			_ = d.Cancel(t0)
			return d, id
		}, ErrDocumentClosed},
		{"rejected document", func(t *testing.T) (*Document, string) {
			d := newDoc(t, 2, DocumentInProgress)
			_ = d.Reject(d.Signers[0].ID, "no", t0)
			return d, d.Signers[1].ID
		}, ErrDocumentClosed},
		{"rejected signer", func(t *testing.T) (*Document, string) {
			d := newDoc(t, 2, DocumentInProgress)
			_ = d.Reject(d.Signers[0].ID, "no", t0)
			return d, d.Signers[0].ID
		}, ErrDocumentClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, id := tc.setup(t)
			before := fmt.Sprint(signerStatuses(d), d.Status)
			_, err := d.Approve(id, t0)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if after := fmt.Sprint(signerStatuses(d), d.Status); after != before {
				t.Fatalf("state changed on error: %s -> %s", before, after)
			}
		})
	}
}

func TestApproveIdempotent(t *testing.T) {
	d := newDoc(t, 2, DocumentInProgress)
	first, err := d.Approve(d.Signers[0].ID, t0)
	if err != nil || first.AlreadySigned {
		t.Fatalf("first: %+v %v", first, err)
	}
	decided := *d.Signers[0].DecidedAt
	again, err := d.Approve(d.Signers[0].ID, t0.Add(time.Hour))
	if err != nil || !again.AlreadySigned {
		t.Fatalf("again: %+v %v", again, err)
	}
	if !d.Signers[0].DecidedAt.Equal(decided) {
		t.Fatal("decided_at changed on repeat")
	}
}

func TestApproveIdempotentAfterCompletion(t *testing.T) {
	d := newDoc(t, 1, DocumentInProgress)
	if _, err := d.Approve(d.Signers[0].ID, t0); err != nil {
		t.Fatal(err)
	}
	res, err := d.Approve(d.Signers[0].ID, t0.Add(time.Hour))
	if err != nil || !res.AlreadySigned {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestReject(t *testing.T) {
	tests := []struct {
		name    string
		reason  string
		target  int
		wantErr error
	}{
		{"pending signer", "wrong amount", 0, nil},
		{"waiting signer", "wrong amount", 1, ErrNotYourTurn},
		{"empty reason", "  ", 0, &ValidationError{}},
		{"reason too long", strings.Repeat("x", 501), 0, &ValidationError{}},
		{"reason max length", strings.Repeat("x", 500), 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newDoc(t, 2, DocumentInProgress)
			err := d.Reject(d.Signers[tc.target].ID, tc.reason, t0)
			var ve *ValidationError
			switch want := tc.wantErr.(type) {
			case *ValidationError:
				_ = want
				if !errors.As(err, &ve) {
					t.Fatalf("got %v, want validation error", err)
				}
				if d.Status != DocumentInProgress {
					t.Fatal("state changed on validation error")
				}
				return
			default:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got %v, want %v", err, tc.wantErr)
				}
			}
			if tc.wantErr == nil {
				if d.Status != DocumentRejected || d.Signers[0].Status != SignerRejected || d.Signers[0].RejectReason == "" {
					t.Fatalf("unexpected state %s %v", d.Status, signerStatuses(d))
				}
				if d.Signers[1].Status != SignerWaiting {
					t.Fatal("later signer must stay waiting")
				}
			}
		})
	}
}

func TestRejectAfterSignedOrClosed(t *testing.T) {
	d := newDoc(t, 2, DocumentInProgress)
	_, _ = d.Approve(d.Signers[0].ID, t0)
	if err := d.Reject(d.Signers[0].ID, "late", t0); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("signed signer reject: %v", err)
	}
	_ = d.Cancel(t0)
	if err := d.Reject(d.Signers[1].ID, "late", t0); !errors.Is(err, ErrDocumentClosed) {
		t.Fatalf("cancelled doc reject: %v", err)
	}
	if err := d.Reject(d.Signers[1].ID, "late", t0); !errors.Is(err, ErrDocumentClosed) {
		t.Fatalf("repeat reject: %v", err)
	}
}

func TestTerminalImmutability(t *testing.T) {
	makers := map[DocumentStatus]func(*Document){
		DocumentCompleted: func(d *Document) {
			for _, s := range d.Signers {
				_, _ = d.Approve(s.ID, t0)
			}
		},
		DocumentRejected:  func(d *Document) { _ = d.Reject(d.Signers[0].ID, "no", t0) },
		DocumentCancelled: func(d *Document) { _ = d.Cancel(t0) },
	}
	for status, make := range makers {
		t.Run(string(status), func(t *testing.T) {
			d := newDoc(t, 2, DocumentInProgress)
			make(d)
			if d.Status != status {
				t.Fatalf("setup produced %s", d.Status)
			}
			before := fmt.Sprint(signerStatuses(d), d.Status, d.UpdatedAt)
			if err := d.Send(t0.Add(time.Hour)); !errors.Is(err, ErrDocumentClosed) {
				t.Fatalf("send: %v", err)
			}
			if err := d.Cancel(t0.Add(time.Hour)); !errors.Is(err, ErrDocumentClosed) {
				t.Fatalf("cancel: %v", err)
			}
			for _, s := range d.Signers {
				if s.Status == SignerSigned {
					continue
				}
				if _, err := d.Approve(s.ID, t0.Add(time.Hour)); !errors.Is(err, ErrDocumentClosed) {
					t.Fatalf("approve: %v", err)
				}
				if err := d.Reject(s.ID, "x", t0.Add(time.Hour)); !errors.Is(err, ErrDocumentClosed) {
					t.Fatalf("reject: %v", err)
				}
			}
			if after := fmt.Sprint(signerStatuses(d), d.Status, d.UpdatedAt); after != before {
				t.Fatalf("terminal doc mutated: %s -> %s", before, after)
			}
		})
	}
}

func TestAtMostOnePendingThroughFlow(t *testing.T) {
	d := newDoc(t, 5, DocumentInProgress)
	for _, s := range append([]Signer(nil), d.Signers...) {
		if err := d.CheckInvariants(); err != nil {
			t.Fatal(err)
		}
		pending := 0
		for _, x := range d.Signers {
			if x.Status == SignerPending {
				pending++
			}
		}
		if pending != 1 {
			t.Fatalf("pending=%d", pending)
		}
		if _, err := d.Approve(s.ID, t0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckInvariantsDetectsViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(d *Document)
	}{
		{"two pending", func(d *Document) { d.Signers[1].Status = SignerPending }},
		{"gap in order", func(d *Document) { d.Signers[2].Order = 4 }},
		{"order not starting at 1", func(d *Document) {
			for i := range d.Signers {
				d.Signers[i].Order++
			}
		}},
		{"in_progress without pending", func(d *Document) { d.Signers[0].Status = SignerSigned }},
		{"pending in draft", func(d *Document) { d.Status = DocumentDraft }},
		{"no signers", func(d *Document) { d.Signers = nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newDoc(t, 3, DocumentInProgress)
			tc.mutate(d)
			if err := d.CheckInvariants(); err == nil {
				t.Fatal("expected violation")
			}
		})
	}
}

func TestSignerCountBounds(t *testing.T) {
	mk := func(n int) []SignerInput {
		in := make([]SignerInput, n)
		for i := range in {
			in[i] = SignerInput{FullName: "N", Email: fmt.Sprintf("u%d@x.kz", i)}
		}
		return in
	}
	tests := []struct {
		n  int
		ok bool
	}{{0, false}, {1, true}, {5, true}, {10, true}, {11, false}}
	for _, tc := range tests {
		t.Run(fmt.Sprint(tc.n), func(t *testing.T) {
			c := 0
			signers, err := NewSigners("d", mk(tc.n), func() string { c++; return fmt.Sprint(c) })
			var ve *ValidationError
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				for i, s := range signers {
					if s.Order != i+1 || s.Status != SignerWaiting {
						t.Fatalf("bad signer %+v", s)
					}
				}
				return
			}
			if !errors.As(err, &ve) || ve.Fields["signers"] == "" {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestValidateSignerInputs(t *testing.T) {
	tests := []struct {
		name  string
		in    []SignerInput
		field string
	}{
		{"missing name", []SignerInput{{"", "a@x.kz"}}, "signers[0].full_name"},
		{"bad email", []SignerInput{{"A", "not-an-email"}}, "signers[0].email"},
		{"display name email", []SignerInput{{"A", "A <a@x.kz>"}}, "signers[0].email"},
		{"duplicate email case-insensitive", []SignerInput{{"A", "a@x.kz"}, {"B", "A@X.kz"}}, "signers[1].email"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ve *ValidationError
			if err := ValidateSignerInputs(tc.in); !errors.As(err, &ve) || ve.Fields[tc.field] == "" {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestValidateTitle(t *testing.T) {
	tests := []struct {
		title string
		ok    bool
	}{{"", false}, {"  ", false}, {"a", true}, {strings.Repeat("я", 200), true}, {strings.Repeat("a", 201), false}}
	for _, tc := range tests {
		if got := ValidateTitle(tc.title) == nil; got != tc.ok {
			t.Errorf("title len %d: ok=%v want %v", len(tc.title), got, tc.ok)
		}
	}
}

func TestValidationErrorMessage(t *testing.T) {
	e := NewValidationError(map[string]string{"b": "y", "a": "x"})
	if e.Error() != "validation failed: a: x; b: y" {
		t.Fatal(e.Error())
	}
}
