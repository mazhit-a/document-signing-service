package storage

import (
	"context"
	"errors"
	"io"
)

var (
	ErrNotFound   = errors.New("storage: object not found")
	ErrInvalidKey = errors.New("storage: invalid key")
)

type FileStorage interface {
	Put(ctx context.Context, r io.Reader) (key string, size int64, err error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}
