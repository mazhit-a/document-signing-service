package service

import (
	"crypto/rand"
	"fmt"
	"time"
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type UUIDGenerator struct{}

func (UUIDGenerator) NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
