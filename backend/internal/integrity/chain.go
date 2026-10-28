package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

var GenesisHash = strings.Repeat("0", 64)

type Event struct {
	ID        int64
	Type      string
	Actor     string
	Data      map[string]any
	CreatedAt time.Time
	PrevHash  string
	Hash      string
}

type ChainReport struct {
	OK                 bool
	Events             int
	FirstBrokenEventID *int64
}

func normalizeData(data map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(data))
	for k, v := range data {
		switch x := v.(type) {
		case string:
			out[k] = x
		case int:
			out[k] = int64(x)
		case int32:
			out[k] = int64(x)
		case int64:
			out[k] = x
		case float64:
			if x != math.Trunc(x) || math.Abs(x) > 1<<53 {
				return nil, fmt.Errorf("data key %q: non-integer number", k)
			}
			out[k] = int64(x)
		case json.Number:
			i, err := x.Int64()
			if err != nil {
				return nil, fmt.Errorf("data key %q: non-integer number", k)
			}
			out[k] = i
		default:
			return nil, fmt.Errorf("data key %q: unsupported type %T", k, v)
		}
	}
	return out, nil
}

func CanonicalEvent(e Event) ([]byte, error) {
	data, err := normalizeData(e.Data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"type":       e.Type,
		"actor":      e.Actor,
		"data":       data,
		"created_at": FormatMicro(e.CreatedAt),
	})
}

func ChainHash(prevHash string, e Event) (string, error) {
	canonical, err := CanonicalEvent(e)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func VerifyChain(events []Event) ChainReport {
	report := ChainReport{OK: true, Events: len(events)}
	expectedPrev := GenesisHash
	for _, e := range events {
		broken := e.PrevHash != expectedPrev
		if !broken {
			want, err := ChainHash(e.PrevHash, e)
			broken = err != nil || want != e.Hash
		}
		if broken {
			id := e.ID
			report.OK = false
			report.FirstBrokenEventID = &id
			return report
		}
		expectedPrev = e.Hash
	}
	return report
}
