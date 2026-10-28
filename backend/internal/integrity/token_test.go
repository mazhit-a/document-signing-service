package integrity

import (
	"encoding/base64"
	"testing"
)

func TestNewToken(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 43 {
			t.Fatalf("len = %d", len(tok))
		}
		raw, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil || len(raw) != 32 {
			t.Fatalf("not 32-byte base64url: %v", err)
		}
		if _, dup := seen[tok]; dup {
			t.Fatal("duplicate token")
		}
		seen[tok] = struct{}{}
	}
}

func TestHashToken(t *testing.T) {
	got := HashToken("abc")
	if got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("HashToken = %s", got)
	}
	if HashToken("abc") == HashToken("abd") {
		t.Fatal("different tokens collide")
	}
	if len(got) != 64 {
		t.Fatalf("len = %d", len(got))
	}
}
