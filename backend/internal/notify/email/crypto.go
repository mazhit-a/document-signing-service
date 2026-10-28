package email

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	MinKeyBytes = 32
	hkdfInfo    = "mini-kedo/email-outbox/v1"
	nonceSize   = 12
)

var ErrDecrypt = errors.New("email payload cannot be decrypted")

type Crypto struct {
	aead cipher.AEAD
}

func NewCrypto(secret string) (*Crypto, error) {
	if len(secret) < MinKeyBytes {
		return nil, fmt.Errorf("email outbox key must be at least %d bytes", MinKeyBytes)
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, hkdfInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("derive email outbox key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("email outbox cipher: %w", err)
	}
	aead, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	if err != nil {
		return nil, fmt.Errorf("email outbox gcm: %w", err)
	}
	return &Crypto{aead: aead}, nil
}

func aad(id string, kind Kind) []byte {
	return []byte(id + "|" + string(kind))
}

func (c *Crypto) Seal(id string, kind Kind, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceSize, nonceSize+len(plaintext)+c.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("email outbox nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, aad(id, kind)), nil
}

func (c *Crypto) Open(id string, kind Kind, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < nonceSize+c.aead.Overhead() {
		return nil, ErrDecrypt
	}
	plain, err := c.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], aad(id, kind))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}
