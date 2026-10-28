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

type signHTTP struct {
	*docEnv
	docID  string
	tokens []string
	sigs   *fakes.Signatures
}

func newSignHTTP(t *testing.T) *signHTTP {
	t.Helper()
	clock := &fakes.Clock{T: time.Date(2026, 10, 28, 5, 12, 3, 123456789, time.UTC)}
	tokens, err := auth.NewTokenManager(jwtSecret, time.Hour, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	docs := fakes.NewDocs()
	audit := &fakes.Audit{}
	sigs := &fakes.Signatures{}
	signers := &fakes.Signers{Docs: docs}
	files := fakes.NewStorage()
	ids := &fakes.SeqIDs{}
	logger := testLogger(&bytes.Buffer{})
	docSvc := service.NewDocumentService(docs, sigs, audit, files, fakes.Tx{}, clock, ids, maxUp)
	life := service.NewLifecycleService(docs, signers, audit, fakes.Tx{}, clock, notify.Noop{}, "http://web.test")
	seal := []byte("0123456789abcdef0123456789abcdef")
	sign := service.NewSigningService(docs, signers, sigs, audit, files, fakes.Tx{}, clock, ids, notify.Noop{}, seal)
	verify := service.NewVerifyService(docs, sigs, audit, files, fakes.Tx{}, clock, seal)
	h := NewRouter(Options{
		Logger: logger, CORSAllowedOrigin: "http://x", MaxBodyBytes: maxUp + multipartOverhead,
		Tokens: tokens, Documents: docSvc, Lifecycle: life, Signing: sign, Verify: verify,
	})
	a, _ := tokens.Issue(ownerA)
	b, _ := tokens.Issue(ownerB)
	e := &docEnv{h: h, clock: clock, tokA: "Bearer " + a, tokB: "Bearer " + b, files: files}
	s := &signHTTP{docEnv: e, sigs: sigs}
	s.docID = createDraft(t, e)
	rec := e.postEmpty("/api/v1/documents/"+s.docID+"/send", e.tokA)
	var resp struct {
		Links []struct {
			URL string `json:"url"`
		} `json:"signing_links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, l := range resp.Links {
		s.tokens = append(s.tokens, strings.TrimPrefix(l.URL, "http://web.test/sign/"))
	}
	return s
}

func (s *signHTTP) do(method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("User-Agent", "http-test")
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func TestSignViewShape(t *testing.T) {
	s := newSignHTTP(t)
	rec := s.do("GET", "/api/v1/sign/"+s.tokens[0], "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var v struct {
		Document struct {
			Title     string
			FileName  string `json:"file_name"`
			SHA256    string
			Status    string
			SizeBytes int64 `json:"size_bytes"`
		}
		Signer struct {
			FullName string `json:"full_name"`
			Order    int
			Total    int
			Status   string
		}
		CanAct bool `json:"can_act"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !v.CanAct || v.Signer.Order != 1 || v.Signer.Total != 2 || v.Signer.Status != "pending" || v.Signer.FullName != "A. Seitov" {
		t.Errorf("signer/can_act: %s", rec.Body.String())
	}
	if v.Document.Status != "in_progress" || v.Document.FileName != "contract.pdf" || len(v.Document.SHA256) != 64 || v.Document.SizeBytes == 0 {
		t.Errorf("document: %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("missing no-store")
	}
	if strings.Contains(rec.Body.String(), "email") || strings.Contains(rec.Body.String(), "hr@x.kz") {
		t.Errorf("leaks other signer data: %s", rec.Body.String())
	}

	v2 := s.do("GET", "/api/v1/sign/"+s.tokens[1], "")
	if !strings.Contains(v2.Body.String(), `"can_act":false`) {
		t.Errorf("waiting signer: %s", v2.Body.String())
	}
}

func TestSignFileDownload(t *testing.T) {
	s := newSignHTTP(t)
	rec := s.do("GET", "/api/v1/sign/"+s.tokens[1]+"/file", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(rec.Body.String(), "%PDF-1.4") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	if rec := s.do("GET", "/api/v1/sign/nope/file", ""); rec.Code != 404 {
		t.Errorf("bad token file: %d", rec.Code)
	}
}

func TestSignApproveFlowAndIdempotency(t *testing.T) {
	s := newSignHTTP(t)
	first := s.do("POST", "/api/v1/sign/"+s.tokens[0]+"/approve", "")
	if first.Code != 200 {
		t.Fatalf("%d %s", first.Code, first.Body.String())
	}
	var r struct {
		SignerStatus   string `json:"signer_status"`
		DocumentStatus string `json:"document_status"`
		SignedAt       string `json:"signed_at"`
		Seal           string `json:"seal"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &r)
	if r.SignerStatus != "signed" || r.DocumentStatus != "in_progress" || len(r.Seal) != 64 || !strings.HasSuffix(r.SignedAt, "Z") {
		t.Errorf("body %s", first.Body.String())
	}
	if len(s.sigs.Items) != 1 || s.sigs.Items[0].UserAgent != "http-test" || s.sigs.Items[0].IP != "192.0.2.1" {
		t.Errorf("signature meta: %+v", s.sigs.Items)
	}

	again := s.do("POST", "/api/v1/sign/"+s.tokens[0]+"/approve", "")
	if again.Code != 200 || again.Body.String() != first.Body.String() || len(s.sigs.Items) != 1 {
		t.Errorf("repeat: %d %s", again.Code, again.Body.String())
	}

	last := s.do("POST", "/api/v1/sign/"+s.tokens[1]+"/approve", "")
	if last.Code != 200 || !strings.Contains(last.Body.String(), `"document_status":"completed"`) {
		t.Errorf("last: %d %s", last.Code, last.Body.String())
	}
	view := s.do("GET", "/api/v1/sign/"+s.tokens[1], "")
	if !strings.Contains(view.Body.String(), `"can_act":false`) {
		t.Errorf("view after completion: %s", view.Body.String())
	}
	if rec := s.do("POST", "/api/v1/sign/"+s.tokens[0]+"/reject", `{"reason":"x"}`); rec.Code != 409 || errCode(rec) != "document_closed" {
		t.Errorf("reject after completion: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSignMatrix(t *testing.T) {
	s := newSignHTTP(t)
	if rec := s.do("POST", "/api/v1/sign/"+s.tokens[1]+"/approve", ""); rec.Code != 409 || errCode(rec) != "not_your_turn" {
		t.Errorf("waiting approve: %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.do("POST", "/api/v1/sign/"+s.tokens[1]+"/reject", `{"reason":"x"}`); rec.Code != 409 || errCode(rec) != "not_your_turn" {
		t.Errorf("waiting reject: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"", "/file", "/approve", "/reject"} {
		method := "POST"
		if p == "" || p == "/file" {
			method = "GET"
		}
		rec := s.do(method, "/api/v1/sign/not-a-real-token"+p, `{"reason":"x"}`)
		if rec.Code != 404 || errCode(rec) != "not_found" {
			t.Errorf("%s%s unknown: %d %s", method, p, rec.Code, rec.Body.String())
		}
	}
}

func TestSignRejectFlow(t *testing.T) {
	s := newSignHTTP(t)
	for name, body := range map[string]string{
		"empty": `{"reason":""}`, "missing": `{}`, "long": `{"reason":"` + strings.Repeat("a", 501) + `"}`,
	} {
		rec := s.do("POST", "/api/v1/sign/"+s.tokens[0]+"/reject", body)
		if rec.Code != 400 || errCode(rec) != "validation_failed" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := s.do("POST", "/api/v1/sign/"+s.tokens[0]+"/reject", `not json`); rec.Code != 400 || errCode(rec) != "bad_request" {
		t.Errorf("malformed: %d %s", rec.Code, rec.Body.String())
	}

	rec := s.do("POST", "/api/v1/sign/"+s.tokens[0]+"/reject", `{"reason":"Wrong salary amount"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"signer_status":"rejected"`) || !strings.Contains(rec.Body.String(), `"document_status":"rejected"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for _, tok := range s.tokens {
		for _, act := range []string{"/approve", "/reject"} {
			rec := s.do("POST", "/api/v1/sign/"+tok+act, `{"reason":"x"}`)
			if rec.Code != 409 || errCode(rec) != "document_closed" {
				t.Errorf("%s after reject: %d %s", act, rec.Code, rec.Body.String())
			}
		}
		if v := s.do("GET", "/api/v1/sign/"+tok, ""); !strings.Contains(v.Body.String(), `"can_act":false`) {
			t.Errorf("view: %s", v.Body.String())
		}
	}
	if len(s.sigs.Items) != 0 {
		t.Error("no signatures expected")
	}
}

func TestSignCancelledTokensAre404(t *testing.T) {
	s := newSignHTTP(t)
	if rec := s.postEmpty("/api/v1/documents/"+s.docID+"/cancel", s.tokA); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec := s.do("GET", "/api/v1/sign/"+s.tokens[0], ""); rec.Code != 404 {
		t.Errorf("view: %d", rec.Code)
	}
	if rec := s.do("POST", "/api/v1/sign/"+s.tokens[0]+"/approve", ""); rec.Code != 404 {
		t.Errorf("approve: %d", rec.Code)
	}
}

func TestVerifyEndpoint(t *testing.T) {
	s := newSignHTTP(t)
	for _, tok := range s.tokens {
		if rec := s.do("POST", "/api/v1/sign/"+tok+"/approve", ""); rec.Code != 200 {
			t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec := s.get("/api/v1/documents/"+s.docID+"/verify", s.tokA)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var v struct {
		Valid bool `json:"valid"`
		File  struct {
			OK bool `json:"ok"`
		} `json:"file"`
		Signatures []struct {
			SealOK bool `json:"seal_ok"`
			Match  bool `json:"hash_matches_document"`
		} `json:"signatures"`
		AuditChain struct {
			OK                 bool   `json:"ok"`
			Events             int    `json:"events"`
			FirstBrokenEventID *int64 `json:"first_broken_event_id"`
		} `json:"audit_chain"`
		CheckedAt string `json:"checked_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !v.Valid || !v.File.OK || len(v.Signatures) != 2 || !v.Signatures[0].SealOK || !v.Signatures[0].Match || !v.AuditChain.OK || v.AuditChain.FirstBrokenEventID != nil || v.CheckedAt == "" {
		t.Fatalf("verify = %+v", v)
	}

	if rec := s.get("/api/v1/documents/"+s.docID+"/verify", ""); rec.Code != 401 {
		t.Errorf("no auth: %d", rec.Code)
	}
	if rec := s.get("/api/v1/documents/"+s.docID+"/verify", s.tokB); rec.Code != 404 {
		t.Errorf("foreign owner: %d", rec.Code)
	}
}
