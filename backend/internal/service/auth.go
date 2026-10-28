package service

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

const (
	MinPasswordLen = 8
	MaxPasswordLen = 72
	MaxFullNameLen = 100
	MaxEmailLen    = 254
)

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(hash, password string) bool
}

type TokenIssuer interface {
	Issue(userID string) (string, error)
	TTL() time.Duration
}

type LoginResult struct {
	AccessToken string
	ExpiresIn   time.Duration
	User        domain.User
}

type AuthService struct {
	users     UserRepository
	hasher    PasswordHasher
	tokens    TokenIssuer
	clock     Clock
	ids       IDGenerator
	dummyHash string
}

func NewAuthService(users UserRepository, hasher PasswordHasher, tokens TokenIssuer, clock Clock, ids IDGenerator) (*AuthService, error) {
	dummy, err := hasher.Hash("timing-equalizer-password")
	if err != nil {
		return nil, err
	}
	return &AuthService{users: users, hasher: hasher, tokens: tokens, clock: clock, ids: ids, dummyHash: dummy}, nil
}

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func validEmail(email string) bool {
	if email == "" || len(email) > MaxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || addr.Name != "" {
		return false
	}
	at := strings.LastIndexByte(email, '@')
	return at > 0 && strings.Contains(email[at+1:], ".")
}

func (s *AuthService) Register(ctx context.Context, email, password, fullName string) (domain.User, error) {
	email = normalizeEmail(email)
	fullName = strings.TrimSpace(fullName)
	fields := map[string]string{}
	if !validEmail(email) {
		fields["email"] = "must be a valid email address"
	}
	switch {
	case utf8.RuneCountInString(password) < MinPasswordLen:
		fields["password"] = "must be at least 8 characters"
	case len(password) > MaxPasswordLen:
		fields["password"] = "must be at most 72 bytes"
	}
	if n := utf8.RuneCountInString(fullName); n < 1 || n > MaxFullNameLen {
		fields["full_name"] = "must be between 1 and 100 characters"
	}
	if len(fields) > 0 {
		return domain.User{}, domain.NewValidationError(fields)
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return domain.User{}, err
	}
	return s.users.Create(ctx, domain.User{
		ID:           s.ids.NewID(),
		Email:        email,
		PasswordHash: hash,
		FullName:     fullName,
		CreatedAt:    s.clock.Now().UTC().Truncate(time.Microsecond),
	})
}

func (s *AuthService) Login(ctx context.Context, email, password string) (LoginResult, error) {
	email = normalizeEmail(email)
	fields := map[string]string{}
	if email == "" {
		fields["email"] = "required"
	}
	if password == "" {
		fields["password"] = "required"
	}
	if len(fields) > 0 {
		return LoginResult{}, domain.NewValidationError(fields)
	}

	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.hasher.Verify(s.dummyHash, password)
			return LoginResult{}, domain.ErrUnauthorized
		}
		return LoginResult{}, err
	}
	if !s.hasher.Verify(user.PasswordHash, password) {
		return LoginResult{}, domain.ErrUnauthorized
	}
	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{AccessToken: token, ExpiresIn: s.tokens.TTL(), User: user}, nil
}

func (s *AuthService) Me(ctx context.Context, userID string) (domain.User, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.User{}, domain.ErrUnauthorized
		}
		return domain.User{}, err
	}
	return user, nil
}
