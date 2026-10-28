package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

const MinBcryptCost = 10

const DefaultBcryptCost = 12

var ErrWeakCost = errors.New("bcrypt cost below minimum")

type PasswordHasher struct {
	cost int
}

func NewPasswordHasher(cost int) (*PasswordHasher, error) {
	if cost < MinBcryptCost || cost > bcrypt.MaxCost {
		return nil, ErrWeakCost
	}
	return &PasswordHasher{cost: cost}, nil
}

func (h *PasswordHasher) Hash(password string) (string, error) {
	out, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (h *PasswordHasher) Verify(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
