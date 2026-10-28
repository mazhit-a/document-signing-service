package http

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/service"
)

type AuthUseCase interface {
	Register(ctx context.Context, email, password, fullName string) (domain.User, error)
	Login(ctx context.Context, email, password string) (service.LoginResult, error)
	Me(ctx context.Context, userID string) (domain.User, error)
}

type TokenParser interface {
	Parse(token string) (string, error)
}

func UserIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxUserID).(string)
	return id
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

func RequireAuth(parser TokenParser) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				rejectUnauthorized(w, r)
				return
			}
			userID, err := parser.Parse(token)
			if err != nil {
				rejectUnauthorized(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), ctxUserID, userID)
			ctx = context.WithValue(ctx, ctxLogger, LoggerFrom(ctx).With("user_id", userID))
			if st, ok := r.Context().Value(ctxRequestState).(*requestState); ok {
				st.userID = userID
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func rejectUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	WriteError(w, r, domain.ErrUnauthorized)
}

type AuthHandlers struct {
	uc AuthUseCase
}

func NewAuthHandlers(uc AuthUseCase) *AuthHandlers {
	return &AuthHandlers{uc: uc}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FullName  string `json:"full_name"`
	CreatedAt string `json:"created_at"`
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

func toUserResponse(u domain.User) userResponse {
	return userResponse{
		ID:        u.ID,
		Email:     u.Email,
		FullName:  u.FullName,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *AuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	user, err := h.uc.Register(r.Context(), req.Email, req.Password, req.FullName)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, toUserResponse(user))
}

func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	res, err := h.uc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, loginResponse{
		AccessToken: res.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(res.ExpiresIn.Seconds()),
	})
}

func (h *AuthHandlers) Me(w http.ResponseWriter, r *http.Request) {
	user, err := h.uc.Me(r.Context(), UserIDFrom(r.Context()))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toUserResponse(user))
}
