package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/auth"
	"github.com/mazhit-a/mini-kedo/internal/integrity"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres/pgtest"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/storage/localfs"
	transport "github.com/mazhit-a/mini-kedo/internal/transport/http"
)

const (
	jwtSecret  = "e2e-jwt-secret-0123456789abcdef0123456789"
	sealSecret = "0123456789abcdef0123456789abcdef"
	webURL     = "http://web.test"
	maxUpload  = 1 << 20
)

type stack struct {
	srv     *httptest.Server
	db      *pgtest.DB
	dataDir string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	db := pgtest.New(t)
	dir := t.TempDir()
	files, err := localfs.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	clock := service.SystemClock{}
	hasher, err := auth.NewPasswordHasher(auth.MinBcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewTokenManager(jwtSecret, time.Hour, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	txm := postgres.NewTxManager(db.Pool)
	docs := postgres.NewDocumentRepository(db.Pool, txm)
	signers := postgres.NewSignerRepository(db.Pool)
	sigs := postgres.NewSignatureRepository(db.Pool)
	audit := postgres.NewAuditRepository(db.Pool)
	ids := service.UUIDGenerator{}

	authSvc, err := service.NewAuthService(postgres.NewUserRepository(db.Pool), hasher, tokens, clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	h := transport.NewRouter(transport.Options{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		CORSAllowedOrigin: "http://x",
		MaxBodyBytes:      maxUpload + 1<<20,
		Auth:              authSvc,
		Tokens:            tokens,
		Documents:         service.NewDocumentService(docs, sigs, audit, files, txm, clock, ids, maxUpload),
		Lifecycle:         service.NewLifecycleService(docs, signers, audit, txm, clock, notify.Noop{}, webURL),
		Signing:           service.NewSigningService(docs, signers, sigs, audit, files, txm, clock, ids, notify.Noop{}, []byte(sealSecret)),
		Verify:            service.NewVerifyService(docs, sigs, audit, files, txm, clock, []byte(sealSecret)),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &stack{srv: srv, db: db, dataDir: dir}
}

type reply struct {
	status int
	header http.Header
	raw    []byte
}

func (r reply) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.raw, v); err != nil {
		t.Fatalf("decode %s: %v", r.raw, err)
	}
}

func (r reply) object(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	r.decode(t, &m)
	return m
}

func (s *stack) do(t *testing.T, method, path, bearer, contentType string, body io.Reader) reply {
	t.Helper()
	req, err := http.NewRequest(method, s.srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, raw: raw}
}

func (s *stack) json(t *testing.T, method, path, bearer string, body any) reply {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	return s.do(t, method, path, bearer, "application/json", rd)
}

func expectStatus(t *testing.T, r reply, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d; body: %s", r.status, want, r.raw)
	}
}

func requireKeys(t *testing.T, m map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in %v", k, m)
		}
	}
}

func asObject(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %v", v)
	}
	return m
}

func asList(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("not a list: %v", v)
	}
	return l
}

func requireTimestamp(t *testing.T, v any) {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("timestamp is not a string: %v", v)
	}
	if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
		t.Errorf("bad timestamp %q: %v", s, err)
	}
}

func requireErrorEnvelope(t *testing.T, r reply, status int, code string) {
	t.Helper()
	expectStatus(t, r, status)
	m := r.object(t)
	e := asObject(t, m["error"])
	requireKeys(t, e, "code", "message")
	if e["code"] != code {
		t.Errorf("error code = %v, want %s", e["code"], code)
	}
}

func multipartDoc(t *testing.T, title string, signers any, name string, content []byte) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("title", title)
	sj, err := json.Marshal(signers)
	if err != nil {
		t.Fatal(err)
	}
	_ = mw.WriteField("signers", string(sj))
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(content)
	_ = mw.Close()
	return mw.FormDataContentType(), &buf
}

func (s *stack) storedFile(t *testing.T) string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(s.dataDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			found = append(found, p)
		}
		return err
	})
	if err != nil || len(found) != 1 {
		t.Fatalf("stored files = %v, %v", found, err)
	}
	return found[0]
}

type verifyReport struct {
	Valid bool `json:"valid"`
	File  struct {
		OK bool `json:"ok"`
	} `json:"file"`
	Signatures []struct {
		SealOK bool `json:"seal_ok"`
	} `json:"signatures"`
	AuditChain struct {
		OK                 bool   `json:"ok"`
		FirstBrokenEventID *int64 `json:"first_broken_event_id"`
	} `json:"audit_chain"`
}

func (s *stack) verify(t *testing.T, token, id string) (reply, verifyReport) {
	t.Helper()
	r := s.json(t, "GET", "/api/v1/documents/"+id+"/verify", token, nil)
	expectStatus(t, r, 200)
	var rep verifyReport
	r.decode(t, &rep)
	return r, rep
}

func TestFullLifecycleThenTamper(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	reg := s.json(t, "POST", "/api/v1/auth/register", "", map[string]string{"email": "owner@example.com", "password": "correct-horse", "full_name": "Owner"})
	expectStatus(t, reg, 201)
	requireKeys(t, reg.object(t), "id", "email", "full_name", "created_at")
	if strings.Contains(string(reg.raw), "password") {
		t.Errorf("register leaks password data: %s", reg.raw)
	}

	login := s.json(t, "POST", "/api/v1/auth/login", "", map[string]string{"email": "owner@example.com", "password": "correct-horse"})
	expectStatus(t, login, 200)
	lm := login.object(t)
	requireKeys(t, lm, "access_token", "token_type", "expires_in")
	bearer, _ := lm["access_token"].(string)
	if bearer == "" {
		t.Fatal("empty access token")
	}

	me := s.json(t, "GET", "/api/v1/me", bearer, nil)
	expectStatus(t, me, 200)
	requireKeys(t, me.object(t), "id", "email", "full_name", "created_at")
	requireErrorEnvelope(t, s.json(t, "GET", "/api/v1/me", "", nil), 401, "unauthorized")

	content := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("contract "), 300)...)
	ct, body := multipartDoc(t, "Employment contract", []map[string]string{
		{"full_name": "A. Seitov", "email": "a@x.kz"},
		{"full_name": "HR Manager", "email": "hr@x.kz"},
		{"full_name": "Director", "email": "dir@x.kz"},
	}, "contract.pdf", content)
	created := s.do(t, "POST", "/api/v1/documents", bearer, ct, body)
	expectStatus(t, created, 201)
	cm := created.object(t)
	requireKeys(t, cm, "id", "title", "status", "file", "signers", "created_at")
	requireTimestamp(t, cm["created_at"])
	file := asObject(t, cm["file"])
	requireKeys(t, file, "name", "content_type", "size_bytes", "sha256")
	if file["sha256"] != integrity.SHA256Hex(content) || cm["status"] != "draft" {
		t.Errorf("created = %s", created.raw)
	}
	docSigners := asList(t, cm["signers"])
	if len(docSigners) != 3 {
		t.Fatalf("signers = %d", len(docSigners))
	}
	for i, raw := range docSigners {
		sm := asObject(t, raw)
		requireKeys(t, sm, "id", "order", "full_name", "email", "status")
		if sm["status"] != "waiting" || int(sm["order"].(float64)) != i+1 {
			t.Errorf("signer %d = %v", i, sm)
		}
	}
	docID, _ := cm["id"].(string)

	expectStatus(t, s.json(t, "GET", "/api/v1/documents/"+docID+"/file", bearer, nil), 200)
	list := s.json(t, "GET", "/api/v1/documents?limit=10", bearer, nil)
	expectStatus(t, list, 200)
	lst := list.object(t)
	requireKeys(t, lst, "items", "next_cursor", "has_more")
	if len(asList(t, lst["items"])) != 1 || lst["has_more"] != false {
		t.Errorf("list = %s", list.raw)
	}

	send := s.json(t, "POST", "/api/v1/documents/"+docID+"/send", bearer, nil)
	expectStatus(t, send, 200)
	sm := send.object(t)
	requireKeys(t, sm, "status", "signing_links")
	if sm["status"] != "in_progress" {
		t.Errorf("send = %s", send.raw)
	}
	if send.header.Get("Cache-Control") != "no-store" {
		t.Error("send response must be no-store")
	}
	var tokens []string
	for i, raw := range asList(t, sm["signing_links"]) {
		l := asObject(t, raw)
		requireKeys(t, l, "signer_id", "order", "url", "note")
		url, _ := l["url"].(string)
		if !strings.HasPrefix(url, webURL+"/sign/") || int(l["order"].(float64)) != i+1 {
			t.Errorf("link %d = %v", i, l)
		}
		tokens = append(tokens, strings.TrimPrefix(url, webURL+"/sign/"))
	}
	if len(tokens) != 3 {
		t.Fatalf("links = %d", len(tokens))
	}
	requireErrorEnvelope(t, s.json(t, "POST", "/api/v1/documents/"+docID+"/send", bearer, nil), 409, "invalid_state")

	for i, tok := range tokens {
		view := s.json(t, "GET", "/api/v1/sign/"+tok, "", nil)
		expectStatus(t, view, 200)
		vm := view.object(t)
		requireKeys(t, vm, "document", "signer", "can_act")
		requireKeys(t, asObject(t, vm["document"]), "title", "file_name", "size_bytes", "sha256", "status")
		requireKeys(t, asObject(t, vm["signer"]), "full_name", "order", "total", "status")
		if vm["can_act"] != (i == 0) {
			t.Errorf("signer %d can_act = %v", i+1, vm["can_act"])
		}
	}

	requireErrorEnvelope(t, s.json(t, "POST", "/api/v1/sign/"+tokens[2]+"/approve", "", nil), 409, "not_your_turn")
	requireErrorEnvelope(t, s.json(t, "POST", "/api/v1/sign/not-a-real-token/approve", "", nil), 404, "not_found")

	var firstApprove map[string]any
	for i, tok := range tokens {
		ap := s.json(t, "POST", "/api/v1/sign/"+tok+"/approve", "", nil)
		expectStatus(t, ap, 200)
		am := ap.object(t)
		requireKeys(t, am, "signer_status", "document_status", "signed_at", "seal")
		requireTimestamp(t, am["signed_at"])
		wantDoc := "in_progress"
		if i == len(tokens)-1 {
			wantDoc = "completed"
		}
		if am["signer_status"] != "signed" || am["document_status"] != wantDoc {
			t.Errorf("approve %d = %s", i+1, ap.raw)
		}
		if i == 0 {
			firstApprove = am
		}
	}
	again := s.json(t, "POST", "/api/v1/sign/"+tokens[0]+"/approve", "", nil)
	expectStatus(t, again, 200)
	am := again.object(t)
	if am["signed_at"] != firstApprove["signed_at"] || am["seal"] != firstApprove["seal"] || am["signer_status"] != "signed" || am["document_status"] != "completed" {
		t.Errorf("idempotent approve differs:\n%s\n%v", again.raw, firstApprove)
	}

	got := s.json(t, "GET", "/api/v1/documents/"+docID, bearer, nil)
	expectStatus(t, got, 200)
	gm := got.object(t)
	requireKeys(t, gm, "id", "title", "status", "file", "signers", "created_at", "updated_at", "completed_at", "signatures")
	if gm["status"] != "completed" || len(asList(t, gm["signatures"])) != 3 {
		t.Errorf("document = %s", got.raw)
	}
	for _, raw := range asList(t, gm["signatures"]) {
		requireKeys(t, asObject(t, raw), "id", "signer_id", "document_sha256", "seal", "signed_at")
	}

	ev := s.json(t, "GET", "/api/v1/documents/"+docID+"/events", bearer, nil)
	expectStatus(t, ev, 200)
	var events struct {
		Items []struct {
			ID       int64          `json:"id"`
			Type     string         `json:"type"`
			Actor    string         `json:"actor"`
			Data     map[string]any `json:"data"`
			PrevHash string         `json:"prev_hash"`
			Hash     string         `json:"hash"`
		} `json:"items"`
	}
	ev.decode(t, &events)
	var types []string
	for _, e := range events.Items {
		types = append(types, e.Type)
		if len(e.Hash) != 64 || e.Data == nil {
			t.Errorf("event shape: %+v", e)
		}
	}
	if got := strings.Join(types, ","); !strings.HasSuffix(got, "document.sent,signer.approved,signer.approved,signer.approved,document.completed") {
		t.Errorf("events = %s", got)
	}

	r, rep := s.verify(t, bearer, docID)
	vm := r.object(t)
	requireKeys(t, vm, "valid", "checked_at", "file", "signatures", "audit_chain")
	requireTimestamp(t, vm["checked_at"])
	requireKeys(t, asObject(t, vm["file"]), "ok", "expected_sha256", "actual_sha256")
	requireKeys(t, asObject(t, vm["audit_chain"]), "ok", "events", "first_broken_event_id")
	sigList := asList(t, vm["signatures"])
	if len(sigList) != 3 {
		t.Fatalf("signature checks = %d", len(sigList))
	}
	for _, raw := range sigList {
		requireKeys(t, asObject(t, raw), "signer_id", "order", "seal_ok", "hash_matches_document")
	}
	if !rep.Valid || !rep.File.OK || !rep.AuditChain.OK || rep.AuditChain.FirstBrokenEventID != nil {
		t.Fatalf("untouched document must verify: %s", r.raw)
	}

	path := s.storedFile(t)
	if err := os.WriteFile(path, append(bytes.Clone(content), '!'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, rep := s.verify(t, bearer, docID); rep.Valid || rep.File.OK || !rep.AuditChain.OK {
		t.Errorf("tampered file: %+v", rep)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, rep := s.verify(t, bearer, docID); !rep.Valid {
		t.Fatalf("restored file must verify again: %+v", rep)
	}

	var origPayload string
	const firstSig = `SELECT payload FROM signatures WHERE document_id = $1::uuid ORDER BY signed_at LIMIT 1`
	if err := s.db.Pool.QueryRow(ctx, firstSig, docID).Scan(&origPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Pool.Exec(ctx, `UPDATE signatures SET payload = payload || 'x' WHERE document_id = $1::uuid`, docID); err != nil {
		t.Fatal(err)
	}
	if _, rep := s.verify(t, bearer, docID); rep.Valid || rep.Signatures[0].SealOK {
		t.Errorf("tampered payload: %+v", rep)
	}
	if _, err := s.db.Pool.Exec(ctx, `UPDATE signatures SET payload = left(payload, length(payload) - 1) WHERE document_id = $1::uuid`, docID); err != nil {
		t.Fatal(err)
	}
	if _, rep := s.verify(t, bearer, docID); !rep.Valid {
		t.Fatalf("restored payload must verify again: %+v", rep)
	}

	var target int64
	if err := s.db.Pool.QueryRow(ctx, `SELECT id FROM audit_events WHERE document_id = $1::uuid AND type = 'signer.approved' ORDER BY id LIMIT 1`, docID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE audit_events DISABLE TRIGGER audit_events_no_update_delete`,
		`UPDATE audit_events SET actor = 'attacker' WHERE id = ` + strconv.FormatInt(target, 10),
		`ALTER TABLE audit_events ENABLE TRIGGER audit_events_no_update_delete`,
	} {
		if _, err := s.db.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, rep := s.verify(t, bearer, docID); rep.Valid || rep.AuditChain.OK || rep.AuditChain.FirstBrokenEventID == nil || *rep.AuditChain.FirstBrokenEventID != target {
		t.Errorf("tampered audit event: %+v", rep)
	}
}
