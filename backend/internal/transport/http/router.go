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
}

func NewRouter(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)

	if opts.Auth != nil && opts.Tokens != nil {
		auth := NewAuthHandlers(opts.Auth)
		requireAuth := RequireAuth(opts.Tokens)
		mux.HandleFunc("POST /api/v1/auth/register", auth.Register)
		mux.HandleFunc("POST /api/v1/auth/login", auth.Login)
		mux.Handle("GET /api/v1/me", requireAuth(http.HandlerFunc(auth.Me)))
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
