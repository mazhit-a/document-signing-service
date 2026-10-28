package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const minSecretLen = 32

const issuer = "mini-kedo"

var ErrInvalidToken = errors.New("invalid token")

type TokenManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewTokenManager(secret string, ttl time.Duration, now func() time.Time) (*TokenManager, error) {
	if len(secret) < minSecretLen {
		return nil, fmt.Errorf("jwt secret must be at least %d bytes", minSecretLen)
	}
	if ttl <= 0 {
		return nil, errors.New("jwt ttl must be positive")
	}
	if now == nil {
		now = time.Now
	}
	return &TokenManager{secret: []byte(secret), ttl: ttl, now: now}, nil
}

func (m *TokenManager) TTL() time.Duration { return m.ttl }

func (m *TokenManager) Issue(userID string) (string, error) {
	now := m.now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		Issuer:    issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

func (m *TokenManager) Parse(token string) (string, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(issuer),
		jwt.WithTimeFunc(m.now),
	)
	var claims jwt.RegisteredClaims
	parsed, err := parser.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) {
		return m.secret, nil
	})
	if err != nil || !parsed.Valid || claims.Subject == "" {
		return "", ErrInvalidToken
	}
	return claims.Subject, nil
}
