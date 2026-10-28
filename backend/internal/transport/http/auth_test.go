package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/mazhit-a/mini-kedo/internal/auth"
	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

const jwtSecret = "0123456789abcdef0123456789abcdef"

type memUsers struct {
	mu    sync.Mutex
	byID  map[string]domain.User
	email map[string]string
}

func newMemUsers() *memUsers {
	return &memUsers{byID: map[string]domain.User{}, email: map[string]string{}}
}

func (m *memUsers) Create(_ context.Context, u domain.User) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.email[u.Email]; ok {
		return domain.User{}, domain.ErrEmailTaken
	}
	m.byID[u.ID] = u
	m.email[u.Email] = u.ID
	return u, nil
}

func (m *memUsers) GetByID(_ context.Context, id string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (m *memUsers) GetByEmail(_ context.Context, email string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.email[email]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return m.byID[id], nil
}

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

type seqIDs struct{ n int }

func (s *seqIDs) NewID() string {
	s.n++
	return "00000000-0000-4000-8000-" + strings.Repeat("0", 11) + string(rune('0'+s.n))
}

type authEnv struct {
	h      http.Handler
	clock  *fixedClock
	tokens *auth.TokenManager
	logs   *bytes.Buffer
	users  *memUsers
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	clock := &fixedClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	hasher, err := auth.NewPasswordHasher(auth.MinBcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewTokenManager(jwtSecret, time.Hour, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	users := newMemUsers()
	svc, err := service.NewAuthService(users, hasher, tokens, clock, &seqIDs{})
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	h := NewRouter(Options{
		Logger:            testLogger(logs),
		CORSAllowedOrigin: "http://x",
		MaxBodyBytes:      1 << 16,
		Auth:              svc,
		Tokens:            tokens,
	})
	return &authEnv{h: h, clock: clock, tokens: tokens, logs: logs, users: users}
}

func (e *authEnv) do(method, path, body, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

type errResp struct {
	Error struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errResp {
	t.Helper()
	var e errResp
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return e
}

const regBody = `{"email":"HR@Demo.kz","password":"S3cure-pass!","full_name":"HR Demo"}`

func TestRegister(t *testing.T) {
	e := newAuthEnv(t)
	rec := e.do("POST", "/api/v1/auth/register", regBody, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["email"] != "hr@demo.kz" || got["full_name"] != "HR Demo" || got["id"] == "" || got["created_at"] != "2026-10-28T12:00:00Z" {
		t.Fatalf("unexpected body %v", got)
	}
	for k := range got {
		if strings.Contains(k, "password") || strings.Contains(k, "hash") {
			t.Fatalf("response leaks field %q", k)
		}
	}
	if strings.Contains(rec.Body.String(), "$2a$") {
		t.Fatal("response contains bcrypt hash")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	e := newAuthEnv(t)
	e.do("POST", "/api/v1/auth/register", regBody, "")
	rec := e.do("POST", "/api/v1/auth/register", `{"email":"hr@demo.kz","password":"another-pass1","full_name":"X"}`, "")
	if rec.Code != http.StatusConflict || decodeErr(t, rec).Error.Code != "email_taken" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestRegisterValidation(t *testing.T) {
	e := newAuthEnv(t)
	tests := []struct {
		name   string
		body   string
		fields []string
	}{
		{"all bad", `{"email":"nope","password":"short","full_name":""}`, []string{"email", "password", "full_name"}},
		{"bad email", `{"email":"a@b","password":"S3cure-pass!","full_name":"A"}`, []string{"email"}},
		{"display name email", `{"email":"A <a@b.kz>","password":"S3cure-pass!","full_name":"A"}`, []string{"email"}},
		{"short password", `{"email":"a@b.kz","password":"1234567","full_name":"A"}`, []string{"password"}},
		{"long password", `{"email":"a@b.kz","password":"` + strings.Repeat("x", 73) + `","full_name":"A"}`, []string{"password"}},
		{"long name", `{"email":"a@b.kz","password":"S3cure-pass!","full_name":"` + strings.Repeat("n", 101) + `"}`, []string{"full_name"}},
		{"missing fields", `{}`, []string{"email", "password", "full_name"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("POST", "/api/v1/auth/register", tt.body, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
			}
			er := decodeErr(t, rec)
			if er.Error.Code != "validation_failed" {
				t.Fatalf("code = %q", er.Error.Code)
			}
			if len(er.Error.Fields) != len(tt.fields) {
				t.Fatalf("fields = %v, want %v", er.Error.Fields, tt.fields)
			}
			for _, f := range tt.fields {
				if er.Error.Fields[f] == "" {
					t.Fatalf("missing field %q in %v", f, er.Error.Fields)
				}
			}
		})
	}
}

func TestRegisterMalformedBody(t *testing.T) {
	e := newAuthEnv(t)
	for _, body := range []string{``, `not json`, `{"email":"a@b.kz","extra":1}`, regBody + regBody} {
		rec := e.do("POST", "/api/v1/auth/register", body, "")
		if rec.Code != http.StatusBadRequest || decodeErr(t, rec).Error.Code != "bad_request" {
			t.Fatalf("body %q: status=%d %s", body, rec.Code, rec.Body)
		}
	}
}

func login(t *testing.T, e *authEnv, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "password": password})
	return e.do("POST", "/api/v1/auth/login", string(b), "")
}

func TestLogin(t *testing.T) {
	e := newAuthEnv(t)
	e.do("POST", "/api/v1/auth/register", regBody, "")

	rec := login(t, e, "hr@demo.kz", "S3cure-pass!")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	var got loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken == "" || got.TokenType != "Bearer" || got.ExpiresIn != 3600 {
		t.Fatalf("unexpected body %+v", got)
	}

	if rec := login(t, e, "HR@demo.kz ", "S3cure-pass!"); rec.Code != http.StatusOK {
		t.Fatalf("case-insensitive login status = %d", rec.Code)
	}
}

func TestLoginBadCredentials(t *testing.T) {
	e := newAuthEnv(t)
	e.do("POST", "/api/v1/auth/register", regBody, "")

	wrongPass := login(t, e, "hr@demo.kz", "wrong-password")
	unknown := login(t, e, "ghost@demo.kz", "S3cure-pass!")
	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong password": wrongPass, "unknown user": unknown} {
		if rec.Code != http.StatusUnauthorized || decodeErr(t, rec).Error.Code != "unauthorized" {
			t.Fatalf("%s: status=%d body=%s", name, rec.Code, rec.Body)
		}
	}
	if wrongPass.Body.String() != unknown.Body.String() {
		t.Fatal("responses distinguish unknown user from wrong password")
	}
}

func TestLoginValidation(t *testing.T) {
	e := newAuthEnv(t)
	rec := e.do("POST", "/api/v1/auth/login", `{}`, "")
	er := decodeErr(t, rec)
	if rec.Code != http.StatusBadRequest || er.Error.Code != "validation_failed" || er.Error.Fields["email"] == "" || er.Error.Fields["password"] == "" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestMe(t *testing.T) {
	e := newAuthEnv(t)
	reg := e.do("POST", "/api/v1/auth/register", regBody, "")
	var user userResponse
	_ = json.Unmarshal(reg.Body.Bytes(), &user)
	var tok loginResponse
	_ = json.Unmarshal(login(t, e, "hr@demo.kz", "S3cure-pass!").Body.Bytes(), &tok)

	rec := e.do("GET", "/api/v1/me", "", "Bearer "+tok.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	var me userResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if me != user {
		t.Fatalf("me = %+v, want %+v", me, user)
	}
	if strings.Contains(rec.Body.String(), "$2a$") || strings.Contains(rec.Body.String(), "password") {
		t.Fatal("me response leaks credentials")
	}

	if rec := e.do("GET", "/api/v1/me", "", "bearer "+tok.AccessToken); rec.Code != http.StatusOK {
		t.Fatalf("lowercase scheme status = %d", rec.Code)
	}
}

func TestMeRejections(t *testing.T) {
	e := newAuthEnv(t)
	e.do("POST", "/api/v1/auth/register", regBody, "")
	var tok loginResponse
	_ = json.Unmarshal(login(t, e, "hr@demo.kz", "S3cure-pass!").Body.Bytes(), &tok)

	hs512, _ := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.RegisteredClaims{
		Subject: "x", Issuer: "mini-kedo", ExpiresAt: jwt.NewNumericDate(e.clock.t.Add(time.Hour)),
	}).SignedString([]byte(jwtSecret))
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject: "x", Issuer: "mini-kedo", ExpiresAt: jwt.NewNumericDate(e.clock.t.Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	ghost, _ := e.tokens.Issue("00000000-0000-4000-8000-000000000099")

	tests := []struct {
		name  string
		authz string
	}{
		{"no header", ""},
		{"wrong scheme", "Basic " + tok.AccessToken},
		{"empty bearer", "Bearer "},
		{"garbage", "Bearer abc.def.ghi"},
		{"wrong alg hs512", "Bearer " + hs512},
		{"alg none", "Bearer " + none},
		{"deleted user", "Bearer " + ghost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("GET", "/api/v1/me", "", tt.authz)
			if rec.Code != http.StatusUnauthorized || decodeErr(t, rec).Error.Code != "unauthorized" {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
		})
	}

	e.clock.t = e.clock.t.Add(time.Hour + time.Second)
	rec := e.do("GET", "/api/v1/me", "", "Bearer "+tok.AccessToken)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("expired token: status=%d headers=%v", rec.Code, rec.Header())
	}
}

func TestUserIDInLogsAndSecretsNeverLogged(t *testing.T) {
	e := newAuthEnv(t)
	reg := e.do("POST", "/api/v1/auth/register", regBody, "")
	var user userResponse
	_ = json.Unmarshal(reg.Body.Bytes(), &user)
	var tok loginResponse
	_ = json.Unmarshal(login(t, e, "hr@demo.kz", "S3cure-pass!").Body.Bytes(), &tok)
	e.do("GET", "/api/v1/me", "", "Bearer "+tok.AccessToken)
	e.do("POST", "/api/v1/auth/login", `{"email":"hr@demo.kz","password":"bad-password"}`, "")

	found := false
	for _, line := range logLines(t, e.logs) {
		if line["path"] == "/api/v1/me" && line["user_id"] == user.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no access log line with user_id; logs:\n%s", e.logs)
	}

	e.users.mu.Lock()
	hash := e.users.byID[user.ID].PasswordHash
	e.users.mu.Unlock()
	if !strings.HasPrefix(hash, "$2a$") {
		t.Fatalf("stored hash not bcrypt: %q", hash)
	}
	out := e.logs.String()
	for _, secret := range []string{hash, "S3cure-pass!", "bad-password", tok.AccessToken} {
		if strings.Contains(out, secret) {
			t.Fatalf("logs contain secret %q", secret)
		}
	}
}
