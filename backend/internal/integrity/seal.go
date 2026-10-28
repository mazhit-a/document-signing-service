package integrity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const timeLayoutMicro = "2006-01-02T15:04:05.000000Z"

func FormatMicro(t time.Time) string {
	return t.UTC().Truncate(time.Microsecond).Format(timeLayoutMicro)
}

func BuildPayload(documentID, documentSHA256, signerID string, order int, signedAt time.Time) string {
	return strings.Join([]string{
		"v1",
		documentID,
		documentSHA256,
		signerID,
		strconv.Itoa(order),
		FormatMicro(signedAt),
	}, "|")
}

func Seal(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifySeal(secret []byte, payload, seal string) bool {
	got, err := hex.DecodeString(seal)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return hmac.Equal(mac.Sum(nil), got)
}
