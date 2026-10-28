package integrity

import (
	"encoding/json"
	"testing"
	"time"
)

func buildChain(t *testing.T) []Event {
	t.Helper()
	base := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	events := []Event{
		{ID: 10, Type: "document.created", Actor: "owner:1", Data: map[string]any{"title": "NDA", "signers": 2}, CreatedAt: base},
		{ID: 11, Type: "document.sent", Actor: "owner:1", Data: map[string]any{}, CreatedAt: base.Add(time.Second)},
		{ID: 12, Type: "signer.approved", Actor: "signer:a", Data: map[string]any{"order": 1}, CreatedAt: base.Add(2 * time.Second)},
		{ID: 13, Type: "document.completed", Actor: "system", Data: map[string]any{"order": int64(2)}, CreatedAt: base.Add(3 * time.Second)},
	}
	prev := GenesisHash
	for i := range events {
		events[i].PrevHash = prev
		h, err := ChainHash(prev, events[i])
		if err != nil {
			t.Fatal(err)
		}
		events[i].Hash = h
		prev = h
	}
	return events
}

func TestGenesisHash(t *testing.T) {
	if len(GenesisHash) != 64 {
		t.Fatalf("len = %d", len(GenesisHash))
	}
	for _, c := range GenesisHash {
		if c != '0' {
			t.Fatal("genesis not zeros")
		}
	}
}

func TestCanonicalEventSortedCompact(t *testing.T) {
	e := Event{
		Type:      "signer.approved",
		Actor:     "signer:a",
		Data:      map[string]any{"z": "last", "a": 1, "m": int64(7)},
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC),
	}
	got, err := CanonicalEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"actor":"signer:a","created_at":"2026-01-02T03:04:05.123456Z","data":{"a":1,"m":7,"z":"last"},"type":"signer.approved"}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCanonicalEventDataTypes(t *testing.T) {
	tests := []struct {
		name    string
		data    map[string]any
		wantErr bool
	}{
		{"string", map[string]any{"k": "v"}, false},
		{"int", map[string]any{"k": 3}, false},
		{"integral float from JSON", map[string]any{"k": float64(3)}, false},
		{"json number", map[string]any{"k": json.Number("5")}, false},
		{"fractional float", map[string]any{"k": 1.5}, true},
		{"bool", map[string]any{"k": true}, true},
		{"nested", map[string]any{"k": map[string]any{}}, true},
		{"nil", map[string]any{"k": nil}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CanonicalEvent(Event{Data: tt.data})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCanonicalStableAfterJSONRoundTrip(t *testing.T) {
	e := buildChain(t)[0]
	raw, _ := json.Marshal(e.Data)
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	e2 := e
	e2.Data = back
	a, _ := CanonicalEvent(e)
	b, _ := CanonicalEvent(e2)
	if string(a) != string(b) {
		t.Fatalf("canonical differs: %s vs %s", a, b)
	}
}

func TestChainHashDependsOnPrev(t *testing.T) {
	e := buildChain(t)[0]
	a, _ := ChainHash(GenesisHash, e)
	b, _ := ChainHash(SHA256Hex([]byte("x")), e)
	if a == b {
		t.Fatal("prev hash ignored")
	}
}

func TestVerifyChainValid(t *testing.T) {
	r := VerifyChain(buildChain(t))
	if !r.OK || r.Events != 4 || r.FirstBrokenEventID != nil {
		t.Fatalf("report = %+v", r)
	}
}

func TestVerifyChainEmpty(t *testing.T) {
	r := VerifyChain(nil)
	if !r.OK || r.Events != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestVerifyChainTamper(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(ev []Event)
		wantID int64
	}{
		{"flipped data value", func(ev []Event) { ev[2].Data["order"] = 9 }, 12},
		{"changed actor", func(ev []Event) { ev[1].Actor = "owner:2" }, 11},
		{"changed type", func(ev []Event) { ev[0].Type = "document.sent" }, 10},
		{"changed time", func(ev []Event) { ev[3].CreatedAt = ev[3].CreatedAt.Add(time.Microsecond) }, 13},
		{"changed stored hash", func(ev []Event) { ev[1].Hash = GenesisHash }, 11},
		{"broken prev linkage", func(ev []Event) { ev[2].PrevHash = GenesisHash }, 12},
		{"bad genesis", func(ev []Event) { ev[0].PrevHash = SHA256Hex([]byte("x")) }, 10},
		{"removed middle event", func(ev []Event) {}, 12},
		{"unsupported data", func(ev []Event) { ev[1].Data["x"] = 1.5 }, 11},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := buildChain(t)
			tt.mutate(ev)
			if tt.name == "removed middle event" {
				ev = append(ev[:1], ev[2:]...)
			}
			r := VerifyChain(ev)
			if r.OK || r.FirstBrokenEventID == nil || *r.FirstBrokenEventID != tt.wantID {
				t.Fatalf("report = %+v, want first broken %d", r, tt.wantID)
			}
		})
	}
}

func TestVerifyChainReportsFirstOfMultipleBreaks(t *testing.T) {
	ev := buildChain(t)
	ev[3].Actor = "evil"
	ev[1].Actor = "evil"
	r := VerifyChain(ev)
	if r.OK || *r.FirstBrokenEventID != 11 {
		t.Fatalf("report = %+v", r)
	}
}
