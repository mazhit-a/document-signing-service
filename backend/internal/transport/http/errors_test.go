package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

type envelopeResp struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
}

func TestWriteErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"validation", domain.NewValidationError(map[string]string{"signers": "required"}), 400, "validation_failed"},
		{"bad request", domain.ErrBadRequest, 400, "bad_request"},
		{"unsupported type", domain.ErrUnsupportedFileType, 400, "unsupported_file_type"},
		{"file too large", domain.ErrFileTooLarge, 413, "file_too_large"},
		{"unauthorized", domain.ErrUnauthorized, 401, "unauthorized"},
		{"not found", domain.ErrNotFound, 404, "not_found"},
		{"invalid state", domain.ErrInvalidState, 409, "invalid_state"},
		{"not your turn", domain.ErrNotYourTurn, 409, "not_your_turn"},
		{"document closed", domain.ErrDocumentClosed, 409, "document_closed"},
		{"email taken", domain.ErrEmailTaken, 409, "email_taken"},
		{"rate limited", domain.ErrRateLimited, 429, "rate_limited"},
		{"wrapped sentinel", fmt.Errorf("load document: %w", domain.ErrNotFound), 404, "not_found"},
		{"wrapped validation", fmt.Errorf("create: %w", domain.NewValidationError(map[string]string{"title": "x"})), 400, "validation_failed"},
		{"unknown", errors.New("pq: connection refused at 10.0.0.5"), 500, "internal"},
		{"nil-ish wrapped unknown", fmt.Errorf("wrap: %w", errors.New("boom")), 500, "internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), tc.err)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d", rec.Code, tc.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("content-type %q", ct)
			}
			var got envelopeResp
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Error.Code != tc.wantCode || got.Error.Message == "" || got.Error.Fields == nil {
				t.Fatalf("bad envelope: %+v", got)
			}
		})
	}
}

func TestWriteErrorFieldsAndNoLeak(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), domain.NewValidationError(map[string]string{"signers": "required"}))
	var got envelopeResp
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Error.Fields["signers"] != "required" || !strings.Contains(got.Error.Message, "signers: required") {
		t.Fatalf("fields lost: %+v", got)
	}

	rec = httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), fmt.Errorf("SELECT * FROM users: %w", errors.New("pq: secret")))
	body := rec.Body.String()
	if strings.Contains(body, "SELECT") || strings.Contains(body, "secret") {
		t.Fatalf("internals leaked: %s", body)
	}
}

func TestWrappedMessageNotLeaked(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), fmt.Errorf("document 123 owner 456: %w", domain.ErrNotFound))
	if strings.Contains(rec.Body.String(), "456") {
		t.Fatalf("wrap context leaked: %s", rec.Body.String())
	}
}

func TestDecodeJSON(t *testing.T) {
	type body struct {
		Reason string `json:"reason"`
	}
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid", `{"reason":"x"}`, true},
		{"malformed", `{"reason":`, false},
		{"unknown field", `{"reason":"x","extra":1}`, false},
		{"trailing data", `{"reason":"x"}{}`, false},
		{"empty", ``, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b body
			err := DecodeJSON(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.in)), &b)
			if tc.ok != (err == nil) {
				t.Fatalf("err=%v", err)
			}
			if !tc.ok && !errors.Is(err, domain.ErrBadRequest) {
				t.Fatalf("want ErrBadRequest, got %v", err)
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]string{"a": "b"})
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"a":"b"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
