package email_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/notify/email"
)

var samplePayload = email.Payload{
	DocumentTitle: "Employment agreement", OwnerName: "Olga Owner", OwnerEmail: "olga@example.com",
	SignerName: "A. Seitov", SignerEmail: "a@x.kz", Position: 2, Total: 3,
	Link: "http://web.test/sign/abc", Reason: "Wrong salary",
}

func TestRenderTemplates(t *testing.T) {
	turn, err := email.Render(email.KindSignerTurn, "a@x.kz", samplePayload)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Employment agreement", "Olga Owner", "olga@example.com", "signer 2 of 3", "http://web.test/sign/abc", "A. Seitov"} {
		if !strings.Contains(turn.Body, want) {
			t.Errorf("signer email missing %q:\n%s", want, turn.Body)
		}
	}
	if turn.To != "a@x.kz" || turn.Subject != "Please sign: Employment agreement" {
		t.Errorf("turn = %+v", turn)
	}

	done, _ := email.Render(email.KindOwnerCompleted, "olga@example.com", samplePayload)
	if done.Subject != "Completed: Employment agreement" || strings.Contains(done.Body, "/sign/") {
		t.Errorf("completed = %+v", done)
	}

	rej, _ := email.Render(email.KindOwnerRejected, "olga@example.com", samplePayload)
	if rej.Subject != "Rejected: Employment agreement" || !strings.Contains(rej.Body, "a@x.kz") || !strings.Contains(rej.Body, "Wrong salary") || strings.Contains(rej.Body, "/sign/") {
		t.Errorf("rejected = %+v", rej)
	}

	if _, err := email.Render("nonsense", "a@x.kz", samplePayload); !errors.Is(err, email.ErrInvalidKind) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestRenderTruncatesReasonAndFlattensTitle(t *testing.T) {
	p := samplePayload
	p.Reason = strings.Repeat("я", 500)
	p.DocumentTitle = "Line one\r\nBcc: attacker@example.com"
	m, err := email.Render(email.KindOwnerRejected, "olga@example.com", p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Body, strings.Repeat("я", 201)) {
		t.Error("reason not truncated")
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		t.Errorf("subject has line breaks: %q", m.Subject)
	}
}

func TestBuildMIMEHeadersAndInjection(t *testing.T) {
	now := time.Date(2026, 10, 28, 5, 12, 3, 0, time.UTC)
	raw, err := email.BuildMIME("noreply@kedo.test", email.Message{To: "a@x.kz", Subject: "Подпишите: договор", Body: "hello\nworld\n"}, now)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"From: noreply@kedo.test\r\n", "To: a@x.kz\r\n", "Message-ID: <", "@kedo.test>\r\n", "Date: Wed, 28 Oct 2026 05:12:03 +0000\r\n", "MIME-Version: 1.0\r\n", "charset=UTF-8"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "=?utf-8?q?") {
		t.Errorf("subject not encoded:\n%s", text)
	}

	bad := []email.Message{
		{To: "a@x.kz\r\nBcc: evil@example.com", Subject: "s", Body: "b"},
		{To: "a@x.kz", Subject: "s\r\nBcc: evil@example.com", Body: "b"},
		{To: "a@x.kz", Subject: "s\nX: y", Body: "b"},
		{To: "Name <a@x.kz>", Subject: "s", Body: "b"},
		{To: "", Subject: "s", Body: "b"},
	}
	for _, m := range bad {
		if _, err := email.BuildMIME("noreply@kedo.test", m, now); !errors.Is(err, email.ErrInvalidHeader) {
			t.Errorf("message %+v accepted: %v", m, err)
		}
	}
	if _, err := email.BuildMIME("noreply@kedo.test\r\nBcc: x@y.z", email.Message{To: "a@x.kz", Subject: "s"}, now); !errors.Is(err, email.ErrInvalidHeader) {
		t.Errorf("injected from accepted: %v", err)
	}
}

func TestBackoffSchedule(t *testing.T) {
	want := []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		if got := email.Backoff(i + 1); got != w {
			t.Errorf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
	for i := 0; i < 200; i++ {
		d := email.RandomJitter(time.Minute)
		if d < 48*time.Second || d > 72*time.Second {
			t.Fatalf("jitter out of bounds: %v", d)
		}
	}
}
