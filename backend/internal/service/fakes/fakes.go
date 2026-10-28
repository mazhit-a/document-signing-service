package fakes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/storage"
)

type Clock struct{ T time.Time }

func (c *Clock) Now() time.Time { return c.T }

type SeqIDs struct {
	mu sync.Mutex
	n  int
}

func (s *SeqIDs) NewID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.n)
}

type Tx struct{}

func (Tx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

type Docs struct {
	mu       sync.Mutex
	Items    map[string]domain.Document
	CreateFn func() error
}

func NewDocs() *Docs { return &Docs{Items: map[string]domain.Document{}} }

func (d *Docs) Create(_ context.Context, doc domain.Document) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.CreateFn != nil {
		if err := d.CreateFn(); err != nil {
			return err
		}
	}
	d.Items[doc.ID] = doc
	return nil
}

func (d *Docs) GetForOwner(_ context.Context, ownerID, id string) (domain.Document, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	doc, ok := d.Items[id]
	if !ok || doc.OwnerID != ownerID {
		return domain.Document{}, domain.ErrNotFound
	}
	doc.Signers = append([]domain.Signer(nil), doc.Signers...)
	return doc, nil
}

func (d *Docs) GetByID(_ context.Context, id string) (domain.Document, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	doc, ok := d.Items[id]
	if !ok {
		return domain.Document{}, domain.ErrNotFound
	}
	doc.Signers = append([]domain.Signer(nil), doc.Signers...)
	return doc, nil
}

func (d *Docs) GetForUpdate(ctx context.Context, id string) (domain.Document, error) {
	return d.GetByID(ctx, id)
}

func (d *Docs) List(_ context.Context, p service.DocumentListParams) (service.DocumentPage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var items []domain.Document
	for _, doc := range d.Items {
		if doc.OwnerID != p.OwnerID {
			continue
		}
		if p.Status != nil && doc.Status != *p.Status {
			continue
		}
		if p.After != nil {
			if !(doc.CreatedAt.Before(p.After.CreatedAt) || (doc.CreatedAt.Equal(p.After.CreatedAt) && doc.ID < p.After.ID)) {
				continue
			}
		}
		items = append(items, doc)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID > items[j].ID
	})
	page := service.DocumentPage{}
	if len(items) > p.Limit {
		items = items[:p.Limit]
		last := items[len(items)-1]
		page.Next = &service.DocumentCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	page.Items = items
	return page, nil
}

func (d *Docs) UpdateStatus(_ context.Context, id string, status domain.DocumentStatus, updatedAt time.Time, completedAt *time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	doc, ok := d.Items[id]
	if !ok {
		return domain.ErrNotFound
	}
	doc.Status, doc.UpdatedAt, doc.CompletedAt = status, updatedAt, completedAt
	d.Items[id] = doc
	return nil
}

type Signatures struct {
	mu    sync.Mutex
	Items []domain.Signature
}

func (s *Signatures) Insert(_ context.Context, sig domain.Signature) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Items = append(s.Items, sig)
	return nil
}

func (s *Signatures) ListByDocument(_ context.Context, documentID string) ([]domain.Signature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Signature
	for _, sig := range s.Items {
		if sig.DocumentID == documentID {
			out = append(out, sig)
		}
	}
	return out, nil
}

func (s *Signatures) GetBySigner(_ context.Context, signerID string) (domain.Signature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sig := range s.Items {
		if sig.SignerID == signerID {
			return sig, nil
		}
	}
	return domain.Signature{}, domain.ErrNotFound
}

type Audit struct {
	mu     sync.Mutex
	Events []domain.AuditEvent
	Err    error
}

func (a *Audit) Append(_ context.Context, documentID string, typ domain.EventType, actor string, data map[string]any, at time.Time) (domain.AuditEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Err != nil {
		return domain.AuditEvent{}, a.Err
	}
	prev := integrity.GenesisHash
	for _, e := range a.Events {
		if e.DocumentID == documentID {
			prev = e.Hash
		}
	}
	hash, err := integrity.ChainHash(prev, integrity.Event{Type: string(typ), Actor: actor, Data: data, CreatedAt: at})
	if err != nil {
		return domain.AuditEvent{}, err
	}
	ev := domain.AuditEvent{ID: int64(len(a.Events) + 1), DocumentID: documentID, Type: typ, Actor: actor, Data: data, PrevHash: prev, Hash: hash, CreatedAt: at}
	a.Events = append(a.Events, ev)
	return ev, nil
}

func (a *Audit) ListByDocument(_ context.Context, documentID string) ([]domain.AuditEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []domain.AuditEvent
	for _, e := range a.Events {
		if e.DocumentID == documentID {
			out = append(out, e)
		}
	}
	return out, nil
}

type Storage struct {
	mu    sync.Mutex
	Files map[string][]byte
	n     int
}

func NewStorage() *Storage { return &Storage{Files: map[string][]byte{}} }

func (s *Storage) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		buf.Write(chunk[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", 0, fmt.Errorf("write file: %w", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	key := fmt.Sprintf("00000000-0000-4000-8000-%012d", 1000+s.n)
	s.Files[key] = buf.Bytes()
	return key, int64(buf.Len()), nil
}

func (s *Storage) Open(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.Files[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *Storage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Files, key)
	return nil
}

func (s *Storage) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Files)
}

type Signers struct {
	Docs *Docs
}

func (s *Signers) update(id string, fn func(*domain.Signer)) error {
	s.Docs.mu.Lock()
	defer s.Docs.mu.Unlock()
	for docID, doc := range s.Docs.Items {
		for i := range doc.Signers {
			if doc.Signers[i].ID == id {
				fn(&doc.Signers[i])
				s.Docs.Items[docID] = doc
				return nil
			}
		}
	}
	return domain.ErrNotFound
}

func (s *Signers) GetByID(_ context.Context, id string) (domain.Signer, error) {
	s.Docs.mu.Lock()
	defer s.Docs.mu.Unlock()
	for _, doc := range s.Docs.Items {
		for _, sg := range doc.Signers {
			if sg.ID == id {
				return sg, nil
			}
		}
	}
	return domain.Signer{}, domain.ErrNotFound
}

func (s *Signers) GetByTokenHash(_ context.Context, hash string) (domain.Signer, error) {
	s.Docs.mu.Lock()
	defer s.Docs.mu.Unlock()
	for _, doc := range s.Docs.Items {
		for _, sg := range doc.Signers {
			if hash != "" && sg.TokenHash == hash {
				return sg, nil
			}
		}
	}
	return domain.Signer{}, domain.ErrNotFound
}

func (s *Signers) ListByDocument(_ context.Context, documentID string) ([]domain.Signer, error) {
	s.Docs.mu.Lock()
	defer s.Docs.mu.Unlock()
	return append([]domain.Signer(nil), s.Docs.Items[documentID].Signers...), nil
}

func (s *Signers) SetTokenHash(_ context.Context, id, tokenHash string) error {
	return s.update(id, func(sg *domain.Signer) { sg.TokenHash = tokenHash })
}

func (s *Signers) UpdateStatus(_ context.Context, id string, status domain.SignerStatus, decidedAt *time.Time, reason string) error {
	return s.update(id, func(sg *domain.Signer) {
		sg.Status, sg.DecidedAt, sg.RejectReason = status, decidedAt, reason
	})
}

type Notifier struct {
	mu       sync.Mutex
	Requests []notify.SigningRequest
}

func (n *Notifier) SigningRequested(_ context.Context, req notify.SigningRequest) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Requests = append(n.Requests, req)
	return nil
}
