package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/auth"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/service/fakes"
)

const (
	ownerA = "11111111-1111-4111-8111-111111111111"
	ownerB = "22222222-2222-4222-8222-222222222222"
	maxUp  = 1 << 20
)

type docEnv struct {
	h     http.Handler
	clock *fakes.Clock
	tokA  string
	tokB  string
	files *fakes.Storage
}

func newDocEnv(t *testing.T) *docEnv {
	t.Helper()
	clock := &fakes.Clock{T: time.Date(2026, 10, 28, 5, 12, 3, 0, time.UTC)}
	tokens, err := auth.NewTokenManager(jwtSecret, time.Hour, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	files := fakes.NewStorage()
	svc := service.NewDocumentService(fakes.NewDocs(), &fakes.Signatures{}, &fakes.Audit{}, files, fakes.Tx{}, clock, &fakes.SeqIDs{}, maxUp)
	h := NewRouter(Options{
		Logger:            testLogger(&bytes.Buffer{}),
		CORSAllowedOrigin: "http://x",
		MaxBodyBytes:      maxUp + multipartOverhead,
		Tokens:            tokens,
		Documents:         svc,
	})
	a, _ := tokens.Issue(ownerA)
	b, _ := tokens.Issue(ownerB)
	return &docEnv{h: h, clock: clock, tokA: "Bearer " + a, tokB: "Bearer " + b, files: files}
}

type upload struct {
	title    string
	signers  string
	fileName string
	body     []byte
	noFile   bool
}

const goodSigners = `[{"full_name":"A. Seitov","email":"a@x.kz"},{"full_name":"HR Manager","email":"hr@x.kz"}]`

func goodUpload() upload {
	return upload{title: "Employment contract", signers: goodSigners, fileName: "contract.pdf", body: append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("z"), 2048)...)}
}

func (e *docEnv) post(u upload, authz string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("title", u.title)
	if u.signers != "" {
		_ = mw.WriteField("signers", u.signers)
	}
	if !u.noFile {
		fw, _ := mw.CreateFormFile("file", u.fileName)
		_, _ = fw.Write(u.body)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *docEnv) get(path, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestCreateDocumentResponseShape(t *testing.T) {
	e := newDocEnv(t)
	u := goodUpload()
	rec := e.post(u, e.tokA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "title", "status", "file", "signers", "created_at"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if len(raw) != 6 {
		t.Errorf("unexpected extra keys: %v", rec.Body.String())
	}
	var resp struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
		File   struct {
			Name        string `json:"name"`
			ContentType string `json:"content_type"`
			SizeBytes   int64  `json:"size_bytes"`
			SHA256      string `json:"sha256"`
		} `json:"file"`
		Signers []map[string]any `json:"signers"`
		Created string           `json:"created_at"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	sum := sha256.Sum256(u.body)
	if resp.Status != "draft" || resp.File.Name != "contract.pdf" || resp.File.ContentType != "application/pdf" ||
		resp.File.SizeBytes != int64(len(u.body)) || resp.File.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
	if len(resp.Signers) != 2 || resp.Signers[0]["order"] != float64(1) || resp.Signers[1]["order"] != float64(2) ||
		resp.Signers[0]["status"] != "waiting" || resp.Signers[1]["email"] != "hr@x.kz" {
		t.Errorf("unexpected signers: %v", resp.Signers)
	}
	if _, ok := resp.Signers[0]["token_hash"]; ok {
		t.Error("token leaked")
	}
	if resp.Created != "2026-10-28T05:12:03Z" {
		t.Errorf("created_at = %q", resp.Created)
	}
}

func TestCreateDocumentRequiresAuth(t *testing.T) {
	e := newDocEnv(t)
	if rec := e.post(goodUpload(), ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d", rec.Code)
	}
	if rec := e.get("/api/v1/documents", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("list status %d", rec.Code)
	}
}

func TestCreateDocumentValidationFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*upload)
		status int
		code   string
		field  string
	}{
		{"empty title", func(u *upload) { u.title = "" }, 400, "validation_failed", "title"},
		{"title too long", func(u *upload) { u.title = strings.Repeat("a", 201) }, 400, "validation_failed", "title"},
		{"no signers field", func(u *upload) { u.signers = "" }, 400, "validation_failed", "signers"},
		{"signers not json", func(u *upload) { u.signers = "nope" }, 400, "validation_failed", "signers"},
		{"signers not array", func(u *upload) { u.signers = `{"a":1}` }, 400, "validation_failed", "signers"},
		{"empty array", func(u *upload) { u.signers = `[]` }, 400, "validation_failed", "signers"},
		{"eleven signers", func(u *upload) { u.signers = elevenSigners() }, 400, "validation_failed", "signers"},
		{"bad email", func(u *upload) { u.signers = `[{"full_name":"A","email":"bad"}]` }, 400, "validation_failed", "signers[0].email"},
		{"duplicate email", func(u *upload) {
			u.signers = `[{"full_name":"A","email":"a@x.kz"},{"full_name":"B","email":"a@x.kz"}]`
		}, 400, "validation_failed", "signers[1].email"},
		{"missing file", func(u *upload) { u.noFile = true }, 400, "validation_failed", "file"},
		{"bad extension", func(u *upload) { u.fileName = "evil.exe" }, 400, "unsupported_file_type", ""},
		{"mismatched content", func(u *upload) { u.fileName = "fake.pdf"; u.body = []byte("plain text") }, 400, "unsupported_file_type", ""},
		{"oversize", func(u *upload) { u.body = append([]byte("%PDF-"), bytes.Repeat([]byte("a"), maxUp+10)...) }, 413, "file_too_large", ""},
		{"way oversize", func(u *upload) { u.body = append([]byte("%PDF-"), bytes.Repeat([]byte("a"), 3*maxUp)...) }, 413, "file_too_large", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newDocEnv(t)
			u := goodUpload()
			tt.mutate(&u)
			rec := e.post(u, e.tokA)
			if rec.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.status, rec.Body.String())
			}
			er := decodeErr(t, rec)
			if er.Error.Code != tt.code {
				t.Errorf("code %q, want %q", er.Error.Code, tt.code)
			}
			if tt.field != "" {
				if _, ok := er.Error.Fields[tt.field]; !ok {
					t.Errorf("fields %v missing %q", er.Error.Fields, tt.field)
				}
			}
			if e.files.Count() != 0 {
				t.Error("file stored despite failure")
			}
		})
	}
}

func elevenSigners() string {
	var parts []string
	for i := 0; i < 11; i++ {
		parts = append(parts, `{"full_name":"S","email":"`+string(rune('a'+i))+`@x.kz"}`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestCreateDocumentNotMultipart(t *testing.T) {
	e := newDocEnv(t)
	req := httptest.NewRequest("POST", "/api/v1/documents", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", e.tokA)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 400 || decodeErr(t, rec).Error.Code != "bad_request" {
		t.Errorf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func createOK(t *testing.T, e *docEnv, authz string, u upload) map[string]any {
	t.Helper()
	rec := e.post(u, authz)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return m
}

func TestGetDocument(t *testing.T) {
	e := newDocEnv(t)
	created := createOK(t, e, e.tokA, goodUpload())
	id := created["id"].(string)

	rec := e.get("/api/v1/documents/"+id, e.tokA)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m["id"] != id || len(m["signers"].([]any)) != 2 {
		t.Errorf("body %s", rec.Body.String())
	}
	sigs, ok := m["signatures"].([]any)
	if !ok || len(sigs) != 0 {
		t.Errorf("signatures = %v", m["signatures"])
	}
}

func TestForeignAndUnknownIDsReturn404(t *testing.T) {
	e := newDocEnv(t)
	id := createOK(t, e, e.tokA, goodUpload())["id"].(string)
	for _, path := range []string{
		"/api/v1/documents/" + id,
		"/api/v1/documents/" + id + "/file",
		"/api/v1/documents/00000000-0000-4000-8000-00000000ffff",
		"/api/v1/documents/not-a-uuid/file",
	} {
		rec := e.get(path, e.tokB)
		if rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
			t.Errorf("%s: status %d body %s", path, rec.Code, rec.Body.String())
		}
	}
	rec := e.get("/api/v1/documents", e.tokB)
	var list documentListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 0 || list.HasMore {
		t.Errorf("foreign list leaked: %s", rec.Body.String())
	}
}

func TestListPaginationThreePages(t *testing.T) {
	e := newDocEnv(t)
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		e.clock.T = e.clock.T.Add(time.Second)
		want[createOK(t, e, e.tokA, goodUpload())["id"].(string)] = true
	}
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		path := "/api/v1/documents?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rec := e.get(path, e.tokA)
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &raw)
		for _, k := range []string{"items", "next_cursor", "has_more"} {
			if _, ok := raw[k]; !ok {
				t.Fatalf("missing %q: %s", k, rec.Body.String())
			}
		}
		var page documentListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &page)
		pages++
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("duplicate %s", it.ID)
			}
			seen[it.ID] = true
		}
		if !page.HasMore {
			if page.NextCursor != nil {
				t.Error("next_cursor set on last page")
			}
			break
		}
		if page.NextCursor == nil {
			t.Fatal("has_more without next_cursor")
		}
		cursor = *page.NextCursor
	}
	if pages != 3 || len(seen) != 5 {
		t.Errorf("pages=%d seen=%d", pages, len(seen))
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("missing %s", id)
		}
	}
}

func TestListFilterAndParamValidation(t *testing.T) {
	e := newDocEnv(t)
	createOK(t, e, e.tokA, goodUpload())

	var list documentListResponse
	rec := e.get("/api/v1/documents?status=draft", e.tokA)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Errorf("draft filter: %s", rec.Body.String())
	}
	rec = e.get("/api/v1/documents?status=completed", e.tokA)
	list = documentListResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 0 || rec.Code != 200 {
		t.Errorf("completed filter: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("empty items should serialize as []: %s", rec.Body.String())
	}

	for name, q := range map[string]string{
		"bad status":   "status=bogus",
		"bad limit":    "limit=abc",
		"zero limit":   "limit=0",
		"huge limit":   "limit=1000",
		"bad cursor":   "cursor=!!!",
		"junk cursor":  "cursor=aGVsbG8",
		"bad time":     "cursor=" + "eyJ0Ijoibm9wZSIsImkiOiJ4In0",
		"neg limit":    "limit=-3",
		"cursor empty": "cursor=e30",
	} {
		rec := e.get("/api/v1/documents?"+q, e.tokA)
		if rec.Code != 400 || decodeErr(t, rec).Error.Code != "validation_failed" {
			t.Errorf("%s: status %d body %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestDownloadMatchesUpload(t *testing.T) {
	e := newDocEnv(t)
	u := goodUpload()
	u.fileName = "Contract \"final\"; v2\r\n.pdf"
	created := createOK(t, e, e.tokA, u)
	id := created["id"].(string)

	rec := e.get("/api/v1/documents/"+id+"/file", e.tokA)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), u.body) {
		t.Error("downloaded bytes differ from upload")
	}
	sum := sha256.Sum256(rec.Body.Bytes())
	file := created["file"].(map[string]any)
	if hex.EncodeToString(sum[:]) != file["sha256"] {
		t.Error("sha256 of download does not match reported hash")
	}
	if rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers: %v", rec.Header())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || strings.ContainsAny(cd, "\r\n") || strings.Contains(cd, `\"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func TestContentDispositionSanitizes(t *testing.T) {
	tests := []struct{ in, notContain string }{
		{"a\"b.pdf", `"b`},
		{"a/b.pdf", "/"},
		{"a\r\nSet-Cookie: x.pdf", "\n"},
		{"a;b.pdf", ";b"},
	}
	for _, tt := range tests {
		got := contentDisposition(tt.in)
		if strings.Contains(got[len("attachment;"):], tt.notContain) {
			t.Errorf("contentDisposition(%q) = %q", tt.in, got)
		}
	}
	if got := contentDisposition("отчёт.pdf"); !strings.HasPrefix(got, "attachment;") || !strings.Contains(got, "filename") {
		t.Errorf("unicode: %q", got)
	}
}
