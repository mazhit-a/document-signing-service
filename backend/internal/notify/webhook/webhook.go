package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"math/rand/v2"
	"strings"
	"time"
)

const (
	HeaderEvent     = "X-Kedo-Event"
	HeaderDelivery  = "X-Kedo-Delivery"
	HeaderSignature = "X-Kedo-Signature"
	HeaderTimestamp = "X-Kedo-Timestamp"

	MaxAttempts     = 6
	signaturePrefix = "sha256="
	jitterFraction  = 0.2
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusDelivered Status = "delivered"
	StatusFailed    Status = "failed"
)

type Delivery struct {
	ID             string
	IdempotencyKey string
	Attempts       int
	URL            string
	Secret         string
	EventType      string
	DocumentID     string
	EventData      map[string]any
	EventAt        time.Time
}

type Outcome struct {
	Status        Status
	NextAttemptAt time.Time
	Error         string
}

type Store interface {
	ProcessNext(ctx context.Context, now time.Time, handle func(ctx context.Context, d Delivery) (Outcome, error)) (bool, error)
}

var backoffSchedule = []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

func Backoff(failedAttempts int) time.Duration {
	if failedAttempts < 1 {
		failedAttempts = 1
	}
	if failedAttempts > len(backoffSchedule) {
		failedAttempts = len(backoffSchedule)
	}
	return backoffSchedule[failedAttempts-1]
}

func RandomJitter(d time.Duration) time.Duration {
	delta := (rand.Float64()*2 - 1) * jitterFraction * float64(d)
	return d + time.Duration(delta)
}

func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

func Verify(secret string, body []byte, signature string) bool {
	if !strings.HasPrefix(signature, signaturePrefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signature, signaturePrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
