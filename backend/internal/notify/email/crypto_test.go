package email_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/notify/email"
)

const (
	keyA = "0123456789abcdef0123456789abcdef-A"
	keyB = "0123456789abcdef0123456789abcdef-B"
)

func mustCrypto(t *testing.T, key string) *email.Crypto {
	t.Helper()
	c, err := email.NewCrypto(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCryptoRoundTrip(t *testing.T) {
	c := mustCrypto(t, keyA)
	plain := []byte(`{"link":"http://web.test/sign/secret-token"}`)
	sealed, err := c.Seal("row-1", email.KindSignerTurn, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("secret-token")) || bytes.Contains(sealed, []byte("/sign/")) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := c.Open("row-1", email.KindSignerTurn, sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open = %q, %v", got, err)
	}
	other, _ := c.Seal("row-1", email.KindSignerTurn, plain)
	if bytes.Equal(sealed, other) {
		t.Error("nonce must be random: identical ciphertexts")
	}
}

func TestCryptoRejectsWrongKeyTamperingAndWrongAAD(t *testing.T) {
	c := mustCrypto(t, keyA)
	sealed, err := c.Seal("row-1", email.KindSignerTurn, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mustCrypto(t, keyB).Open("row-1", email.KindSignerTurn, sealed); !errors.Is(err, email.ErrDecrypt) {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := c.Open("row-2", email.KindSignerTurn, sealed); !errors.Is(err, email.ErrDecrypt) {
		t.Errorf("different row id: %v", err)
	}
	if _, err := c.Open("row-1", email.KindOwnerCompleted, sealed); !errors.Is(err, email.ErrDecrypt) {
		t.Errorf("different kind: %v", err)
	}
	for i := range sealed {
		tampered := bytes.Clone(sealed)
		tampered[i] ^= 0x01
		if _, err := c.Open("row-1", email.KindSignerTurn, tampered); !errors.Is(err, email.ErrDecrypt) {
			t.Fatalf("tampered byte %d accepted: %v", i, err)
		}
	}
	for _, short := range [][]byte{nil, {}, sealed[:5], sealed[:12]} {
		if _, err := c.Open("row-1", email.KindSignerTurn, short); !errors.Is(err, email.ErrDecrypt) {
			t.Errorf("short ciphertext %d bytes: %v", len(short), err)
		}
	}
}

func TestNewCryptoRequiresLongKey(t *testing.T) {
	for _, key := range []string{"", "short", "0123456789abcdef0123456789abcde"} {
		if _, err := email.NewCrypto(key); err == nil {
			t.Errorf("key %q (%d bytes) accepted", key, len(key))
		}
	}
	if _, err := email.NewCrypto(keyA); err != nil {
		t.Error(err)
	}
}
