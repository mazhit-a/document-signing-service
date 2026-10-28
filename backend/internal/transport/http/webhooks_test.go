package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/auth"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

func newWebhookHTTP(t *testing.T) *docEnv {
	t.Helper()
	clock := &fakes.Clock{T: time.Date(2026, 10, 28, 5, 12, 3, 0, time.UTC)}
	tokens, err := auth.NewTokenManager(jwtSecret, time.Hour, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.NewWebhookService(&fakes.WebhookEndpoints{}, clock, &fakes.SeqIDs{})
	h := NewRouter(Options{Logger: testLogger(&bytes.Buffer{}), CORSAllowedOrigin: "http://x", MaxBodyBytes: maxUp, Tokens: tokens, Webhooks: svc})
	a, _ := tokens.Issue(ownerA)
	b, _ := tokens.Issue(ownerB)
	return &docEnv{h: h, clock: clock, tokA: "Bearer " + a, tokB: "Bearer " + b}
}

func (e *docEnv) do(method, path, body, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestWebhookEndpointsLifecycle(t *testing.T) {
	e := newWebhookHTTP(t)

	rec := e.do("POST", "/api/v1/webhooks", `{"url":"https://example.com/hook"}`, e.tokA)
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		Secret string `json:"secret"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || !strings.HasPrefix(created.Secret, "whsec_") || !created.Active || created.URL != "https://example.com/hook" {
		t.Fatalf("created = %+v", created)
	}

	rec = e.do("GET", "/api/v1/webhooks", "", e.tokA)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), created.Secret) || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("list leaks secret or failed: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list = %s", rec.Body.String())
	}

	if rec = e.do("GET", "/api/v1/webhooks", "", e.tokB); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("other owner list = %s", rec.Body.String())
	}
	if rec = e.do("DELETE", "/api/v1/webhooks/"+created.ID, "", e.tokB); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign delete = %d", rec.Code)
	}
	if rec = e.do("DELETE", "/api/v1/webhooks/"+created.ID, "", e.tokA); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec = e.do("DELETE", "/api/v1/webhooks/"+created.ID, "", e.tokA); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", rec.Code)
	}
}

func TestWebhookEndpointsValidationAndAuth(t *testing.T) {
	e := newWebhookHTTP(t)
	cases := []struct {
		name, method, path, body, authz string
		want                            int
	}{
		{"no auth create", "POST", "/api/v1/webhooks", `{"url":"https://x.test"}`, "", 401},
		{"no auth list", "GET", "/api/v1/webhooks", "", "", 401},
		{"no auth delete", "DELETE", "/api/v1/webhooks/abc", "", "", 401},
		{"bad scheme", "POST", "/api/v1/webhooks", `{"url":"ftp://x.test"}`, e.tokA, 400},
		{"missing url", "POST", "/api/v1/webhooks", `{}`, e.tokA, 400},
		{"unknown field", "POST", "/api/v1/webhooks", `{"url":"https://x.test","evil":1}`, e.tokA, 400},
		{"malformed", "POST", "/api/v1/webhooks", `{`, e.tokA, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if rec := e.do(c.method, c.path, c.body, c.authz); rec.Code != c.want {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}
