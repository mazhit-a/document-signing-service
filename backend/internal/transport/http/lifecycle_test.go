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
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

func newLifeHTTP(t *testing.T) (*docEnv, *bytes.Buffer) {
	t.Helper()
	clock := &fakes.Clock{T: time.Date(2026, 10, 28, 5, 12, 3, 0, time.UTC)}
	tokens, err := auth.NewTokenManager(jwtSecret, time.Hour, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	docs := fakes.NewDocs()
	audit := &fakes.Audit{}
	ids := &fakes.SeqIDs{}
	files := fakes.NewStorage()
	logs := &bytes.Buffer{}
	docSvc := service.NewDocumentService(docs, &fakes.Signatures{}, audit, files, fakes.Tx{}, clock, ids, maxUp)
	life := service.NewLifecycleService(docs, &fakes.Signers{Docs: docs}, audit, fakes.Tx{}, clock, notify.NewLogNotifier(testLogger(logs)), "http://web.test")
	h := NewRouter(Options{
		Logger: testLogger(logs), CORSAllowedOrigin: "http://x", MaxBodyBytes: maxUp + multipartOverhead,
		Tokens: tokens, Documents: docSvc, Lifecycle: life,
	})
	a, _ := tokens.Issue(ownerA)
	b, _ := tokens.Issue(ownerB)
	return &docEnv{h: h, clock: clock, tokA: "Bearer " + a, tokB: "Bearer " + b, files: files}, logs
}

func (e *docEnv) postEmpty(path, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func createDraft(t *testing.T, e *docEnv) string {
	t.Helper()
	rec := e.post(goodUpload(), e.tokA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var r struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	return r.ID
}

func errCode(rec *httptest.ResponseRecorder) string {
	var r struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	return r.Error.Code
}

func TestSendResponseShapeAndNoTokenInLogs(t *testing.T) {
	e, logs := newLifeHTTP(t)
	id := createDraft(t, e)

	rec := e.postEmpty("/api/v1/documents/"+id+"/send", e.tokA)
	if rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
		Note   string `json:"note"`
		Links  []struct {
			SignerID string `json:"signer_id"`
			Order    int    `json:"order"`
			URL      string `json:"url"`
			Note     string `json:"note"`
		} `json:"signing_links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "in_progress" || len(resp.Links) != 1 || resp.Links[0].Order != 1 {
		t.Fatalf("unexpected body %s", rec.Body.String())
	}
	if !strings.Contains(resp.Note, "by email") {
		t.Errorf("missing note about later signers: %q", resp.Note)
	}
	for _, l := range resp.Links {
		if !strings.HasPrefix(l.URL, "http://web.test/sign/") || l.Note != "shown once" || l.SignerID == "" {
			t.Errorf("bad link %+v", l)
		}
		token := strings.TrimPrefix(l.URL, "http://web.test/sign/")
		if strings.Contains(logs.String(), token) {
			t.Error("token leaked into logs")
		}
	}

	get := e.get("/api/v1/documents/"+id, e.tokA)
	if strings.Contains(get.Body.String(), "/sign/") || strings.Contains(get.Body.String(), "token") {
		t.Errorf("document detail must not expose tokens: %s", get.Body.String())
	}
}

func TestSendTwiceIs409InvalidState(t *testing.T) {
	e, _ := newLifeHTTP(t)
	id := createDraft(t, e)
	if rec := e.postEmpty("/api/v1/documents/"+id+"/send", e.tokA); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec := e.postEmpty("/api/v1/documents/"+id+"/send", e.tokA)
	if rec.Code != http.StatusConflict || errCode(rec) != "invalid_state" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestLifecycleAuthAndOwnership(t *testing.T) {
	e, _ := newLifeHTTP(t)
	id := createDraft(t, e)
	for _, p := range []string{"/send", "/cancel"} {
		if rec := e.postEmpty("/api/v1/documents/"+id+p, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anon: %d", p, rec.Code)
		}
		if rec := e.postEmpty("/api/v1/documents/"+id+p, e.tokB); rec.Code != http.StatusNotFound {
			t.Errorf("%s foreign: %d", p, rec.Code)
		}
	}
	if rec := e.get("/api/v1/documents/"+id+"/events", e.tokB); rec.Code != http.StatusNotFound {
		t.Errorf("events foreign: %d", rec.Code)
	}
	if rec := e.get("/api/v1/documents/"+id+"/events", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("events anon: %d", rec.Code)
	}
	if rec := e.postEmpty("/api/v1/documents/not-a-uuid/send", e.tokA); rec.Code != http.StatusNotFound {
		t.Errorf("malformed id: %d", rec.Code)
	}
}

func TestCancelFlowAndEvents(t *testing.T) {
	e, _ := newLifeHTTP(t)
	id := createDraft(t, e)
	e.postEmpty("/api/v1/documents/"+id+"/send", e.tokA)

	rec := e.postEmpty("/api/v1/documents/"+id+"/cancel", e.tokA)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.postEmpty("/api/v1/documents/"+id+"/cancel", e.tokA)
	if rec.Code != http.StatusConflict || errCode(rec) != "document_closed" {
		t.Fatalf("second cancel: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.postEmpty("/api/v1/documents/"+id+"/send", e.tokA)
	if rec.Code != http.StatusConflict || errCode(rec) != "document_closed" {
		t.Fatalf("send after cancel: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.get("/api/v1/documents/"+id+"/events", e.tokA)
	if rec.Code != http.StatusOK {
		t.Fatalf("events: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			ID    int64          `json:"id"`
			Type  string         `json:"type"`
			Actor string         `json:"actor"`
			Data  map[string]any `json:"data"`
		} `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 3 {
		t.Fatalf("events = %s", rec.Body.String())
	}
	want := []string{"document.created", "document.sent", "document.cancelled"}
	for i, it := range resp.Items {
		if it.Type != want[i] || it.Actor != ownerA || (i > 0 && it.ID <= resp.Items[i-1].ID) {
			t.Errorf("event %d = %+v", i, it)
		}
	}
}
