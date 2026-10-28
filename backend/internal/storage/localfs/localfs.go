package localfs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/mazhit-a/mini-kedo/internal/storage"
)

var keyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Storage struct {
	root string
}

func New(root string) (*Storage, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	return &Storage{root: abs}, nil
}

func newKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func (s *Storage) path(key string) (string, error) {
	if !keyPattern.MatchString(key) {
		return "", storage.ErrInvalidKey
	}
	return filepath.Join(s.root, key), nil
}

func (s *Storage) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	key, err := newKey()
	if err != nil {
		return "", 0, fmt.Errorf("generate key: %w", err)
	}
	dest, err := s.path(key)
	if err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(s.root, ".upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	size, err := io.Copy(tmp, &ctxReader{ctx: ctx, r: r})
	if err != nil {
		return "", 0, fmt.Errorf("write file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, fmt.Errorf("sync file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("close file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return "", 0, fmt.Errorf("chmod file: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", 0, fmt.Errorf("rename file: %w", err)
	}
	committed = true
	return key, size, nil
}

func (s *Storage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("open file: %w", err)
	}
	return f, nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
