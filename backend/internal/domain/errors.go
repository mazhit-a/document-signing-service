package domain

import (
	"errors"
	"sort"
	"strings"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrEmailTaken          = errors.New("email already taken")
	ErrInvalidState        = errors.New("invalid state")
	ErrNotYourTurn         = errors.New("not your turn")
	ErrDocumentClosed      = errors.New("document closed")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrBadRequest          = errors.New("bad request")
	ErrFileTooLarge        = errors.New("file too large")
	ErrUnsupportedFileType = errors.New("unsupported file type")
	ErrRateLimited         = errors.New("rate limited")
)

type ValidationError struct {
	Fields map[string]string
}

func NewValidationError(fields map[string]string) *ValidationError {
	copied := make(map[string]string, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	return &ValidationError{Fields: copied}
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e.Fields[k])
	}
	return "validation failed: " + strings.Join(parts, "; ")
}
