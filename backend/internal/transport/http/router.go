package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type Options struct {
	Logger            *slog.Logger
	CORSAllowedOrigin string
	MaxBodyBytes      int64
	Auth              AuthUseCase
	Tokens            TokenParser
	Documents         DocumentUseCase
	Lifecycle         LifecycleUseCase
	Signing           SigningUseCase
	Verify            VerifyUseCase
	Webhooks          WebhookUseCase
	ReadyChecks       map[string]ReadyCheck
	LoginLimiter      *Limiter
	SignLimiter       *Limiter
	TrustProxy        bool
}

func NewRouter(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	if len(opts.ReadyChecks) > 0 {
		mux.HandleFunc("GET /readyz", readyz(opts.ReadyChecks))
	}

	if opts.Auth != nil && opts.Tokens != nil {
		auth := NewAuthHandlers(opts.Auth)
		requireAuth := RequireAuth(opts.Tokens)
		mux.HandleFunc("POST /api/v1/auth/register", auth.Register)
		limitLogin := RateLimit(opts.LoginLimiter, opts.TrustProxy)
		mux.Handle("POST /api/v1/auth/login", limitLogin(http.HandlerFunc(auth.Login)))
		mux.Handle("GET /api/v1/me", requireAuth(http.HandlerFunc(auth.Me)))
	}

	if opts.Documents != nil && opts.Tokens != nil {
		docs := NewDocumentHandlers(opts.Documents)
		requireAuth := RequireAuth(opts.Tokens)
		mux.Handle("POST /api/v1/documents", requireAuth(http.HandlerFunc(docs.Create)))
		mux.Handle("GET /api/v1/documents", requireAuth(http.HandlerFunc(docs.List)))
		mux.Handle("GET /api/v1/documents/{id}", requireAuth(http.HandlerFunc(docs.Get)))
		mux.Handle("GET /api/v1/documents/{id}/file", requireAuth(http.HandlerFunc(docs.Download)))
	}

	if opts.Lifecycle != nil && opts.Tokens != nil {
		life := NewLifecycleHandlers(opts.Lifecycle)
		requireAuth := RequireAuth(opts.Tokens)
		mux.Handle("POST /api/v1/documents/{id}/send", requireAuth(http.HandlerFunc(life.Send)))
		mux.Handle("POST /api/v1/documents/{id}/cancel", requireAuth(http.HandlerFunc(life.Cancel)))
		mux.Handle("GET /api/v1/documents/{id}/events", requireAuth(http.HandlerFunc(life.Events)))
	}

	if opts.Verify != nil && opts.Tokens != nil {
		ver := NewVerifyHandlers(opts.Verify)
		requireAuth := RequireAuth(opts.Tokens)
		mux.Handle("GET /api/v1/documents/{id}/verify", requireAuth(http.HandlerFunc(ver.Verify)))
	}

	if opts.Webhooks != nil && opts.Tokens != nil {
		hooks := NewWebhookHandlers(opts.Webhooks)
		requireAuth := RequireAuth(opts.Tokens)
		mux.Handle("POST /api/v1/webhooks", requireAuth(http.HandlerFunc(hooks.Create)))
		mux.Handle("GET /api/v1/webhooks", requireAuth(http.HandlerFunc(hooks.List)))
		mux.Handle("DELETE /api/v1/webhooks/{id}", requireAuth(http.HandlerFunc(hooks.Delete)))
	}

	if opts.Signing != nil {
		sign := NewSigningHandlers(opts.Signing)
		limitSign := RateLimit(opts.SignLimiter, opts.TrustProxy)
		mux.Handle("GET /api/v1/sign/{token}", limitSign(http.HandlerFunc(sign.View)))
		mux.Handle("GET /api/v1/sign/{token}/file", limitSign(http.HandlerFunc(sign.File)))
		mux.Handle("POST /api/v1/sign/{token}/approve", limitSign(http.HandlerFunc(sign.Approve)))
		mux.Handle("POST /api/v1/sign/{token}/reject", limitSign(http.HandlerFunc(sign.Reject)))
	}

	var h http.Handler = mux
	h = BodyLimit(opts.MaxBodyBytes)(h)
	h = CORS(opts.CORSAllowedOrigin)(h)
	h = Recover(h)
	h = AccessLog(h)
	h = RequestID(opts.Logger)(h)
	return h
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
