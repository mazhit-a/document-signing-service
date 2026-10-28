package integrity

import (
	"testing"
	"time"
)

func TestSealKnownVector(t *testing.T) {
	got := Seal([]byte("secret-key"), "abc")
	want := "28c2c4a6a91ebb4773a7b0342b1a5a2897aca4fb7ad7bcb33a6ff91afc7a03b0"
	if got != want {
		t.Fatalf("Seal = %s, want %s", got, want)
	}
}

func TestBuildPayloadAndSealVector(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	payload := BuildPayload("doc-1", "abc", "signer-1", 1, at)
	wantPayload := "v1|doc-1|abc|signer-1|1|2026-01-02T03:04:05.123456Z"
	if payload != wantPayload {
		t.Fatalf("payload = %s, want %s", payload, wantPayload)
	}
	seal := Seal([]byte("secret-key"), payload)
	if seal != "c77ace9fa08400d258aa16865a37c88412509bc6f11e40a830b398330de9bab6" {
		t.Fatalf("seal = %s", seal)
	}
}

func TestBuildPayloadNonUTCAndTruncation(t *testing.T) {
	loc := time.FixedZone("x", 5*3600)
	at := time.Date(2026, 1, 2, 8, 4, 5, 999999999, loc)
	got := BuildPayload("d", "h", "s", 2, at)
	want := "v1|d|h|s|2|2026-01-02T03:04:05.999999Z"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestTimeTruncationRoundTrip(t *testing.T) {
	at := time.Date(2026, 5, 6, 7, 8, 9, 123456789, time.UTC)
	stored := at.Truncate(time.Microsecond)
	p1 := BuildPayload("d", "h", "s", 1, at)
	p2 := BuildPayload("d", "h", "s", 1, stored)
	if p1 != p2 {
		t.Fatalf("payload differs after microsecond truncation")
	}
	parsed, err := time.Parse(timeLayoutMicro, FormatMicro(at))
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(stored) {
		t.Fatalf("round trip %v != %v", parsed, stored)
	}
}

func TestVerifySeal(t *testing.T) {
	secret := []byte("secret-key")
	payload := "v1|doc|sha|signer|1|2026-01-02T03:04:05.000000Z"
	seal := Seal(secret, payload)

	flipped := []byte(payload)
	flipped[5] ^= 0x01

	tests := []struct {
		name    string
		secret  []byte
		payload string
		seal    string
		want    bool
	}{
		{"valid", secret, payload, seal, true},
		{"wrong secret", []byte("other"), payload, seal, false},
		{"flipped payload byte", secret, string(flipped), seal, false},
		{"tampered seal", secret, payload, "00" + seal[2:], false},
		{"non-hex seal", secret, payload, "zzzz", false},
		{"empty seal", secret, payload, "", false},
		{"truncated seal", secret, payload, seal[:10], false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifySeal(tt.secret, tt.payload, tt.seal); got != tt.want {
				t.Fatalf("VerifySeal = %v, want %v", got, tt.want)
			}
		})
	}
}
