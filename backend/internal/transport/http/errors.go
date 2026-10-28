package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

type mappedError struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func MapError(err error) mappedError {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		return mappedError{http.StatusBadRequest, "validation_failed", ve.Error(), ve.Fields}
	case errors.Is(err, domain.ErrNotFound):
		return mappedError{http.StatusNotFound, "not_found", "resource not found", nil}
	case errors.Is(err, domain.ErrUnauthorized):
		return mappedError{http.StatusUnauthorized, "unauthorized", "authentication required", nil}
	case errors.Is(err, domain.ErrEmailTaken):
		return mappedError{http.StatusConflict, "email_taken", "email is already registered", nil}
	case errors.Is(err, domain.ErrNotYourTurn):
		return mappedError{http.StatusConflict, "not_your_turn", "it is not your turn to sign", nil}
	case errors.Is(err, domain.ErrDocumentClosed):
		return mappedError{http.StatusConflict, "document_closed", "document is closed", nil}
	case errors.Is(err, domain.ErrInvalidState):
		return mappedError{http.StatusConflict, "invalid_state", "operation is not allowed in the current state", nil}
	case errors.Is(err, domain.ErrFileTooLarge):
		return mappedError{http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds the size limit", nil}
	case errors.Is(err, domain.ErrUnsupportedFileType):
		return mappedError{http.StatusBadRequest, "unsupported_file_type", "file type is not supported", nil}
	case errors.Is(err, domain.ErrBadRequest):
		return mappedError{http.StatusBadRequest, "bad_request", "malformed request", nil}
	}
	return mappedError{http.StatusInternalServerError, "internal", "internal server error", nil}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	m := MapError(err)
	if m.Status >= http.StatusInternalServerError {
		LoggerFrom(r.Context()).Error("request failed", "error", err.Error())
	}
	writeErrorWithFields(w, m.Status, m.Code, m.Message, m.Fields)
}

func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.ErrBadRequest
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return domain.ErrBadRequest
	}
	return nil
}
