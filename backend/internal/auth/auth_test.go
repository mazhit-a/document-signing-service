package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef"

var fixed = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func manager(t *testing.T, now *time.Time) *TokenManager {
	t.Helper()
	m, err := NewTokenManager(testSecret, time.Hour, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPasswordHasher(t *testing.T) {
	if _, err := NewPasswordHasher(MinBcryptCost - 1); err == nil {
		t.Fatal("cost below minimum accepted")
	}
	h, err := NewPasswordHasher(MinBcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := h.Hash("S3cure-pass!")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$2a$10$") {
		t.Fatalf("unexpected hash format %q", hash[:7])
	}
	if !h.Verify(hash, "S3cure-pass!") || h.Verify(hash, "wrong") {
		t.Fatal("verify mismatch")
	}
}

func TestTokenRoundTrip(t *testing.T) {
	now := fixed
	m := manager(t, &now)
	tok, err := m.Issue("user-1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Parse(tok)
	if err != nil || id != "user-1" {
		t.Fatalf("parse = %q, %v", id, err)
	}
}

func TestTokenRejections(t *testing.T) {
	now := fixed
	m := manager(t, &now)
	good, _ := m.Issue("user-1")

	other, _ := NewTokenManager("ffffffffffffffffffffffffffffffff", time.Hour, func() time.Time { return now })
	wrongKey, _ := other.Issue("user-1")

	hs512, _ := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.RegisteredClaims{
		Subject: "user-1", Issuer: issuer, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString([]byte(testSecret))

	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject: "user-1", Issuer: issuer, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)

	noExp, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject: "user-1", Issuer: issuer,
	}).SignedString([]byte(testSecret))

	wrongIss, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject: "user-1", Issuer: "other", ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString([]byte(testSecret))

	noSub, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: issuer, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString([]byte(testSecret))

	tests := map[string]string{
		"empty":     "",
		"garbage":   "not.a.jwt",
		"wrong key": wrongKey,
		"hs512":     hs512,
		"none":      none,
		"no exp":    noExp,
		"wrong iss": wrongIss,
		"no sub":    noSub,
		"tampered":  good[:len(good)-2] + "xx",
	}
	for name, tok := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := m.Parse(tok); err == nil {
				t.Fatal("token accepted")
			}
		})
	}

	now = fixed.Add(time.Hour + time.Second)
	if _, err := m.Parse(good); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestNewTokenManagerValidation(t *testing.T) {
	if _, err := NewTokenManager("short", time.Hour, nil); err == nil {
		t.Fatal("short secret accepted")
	}
	if _, err := NewTokenManager(testSecret, 0, nil); err == nil {
		t.Fatal("zero ttl accepted")
	}
}
