package email_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/notify/email"
	"github.com/mazhit-a/mini-kedo/internal/notify/email/smtptest"
)

const (
	smtpUser = "mailer@kedo.test"
	smtpPass = "app-password-123"
)

func newSender(t *testing.T, srv *smtptest.Server, mutate func(*email.SMTPConfig)) *email.SMTPSender {
	t.Helper()
	cfg := email.SMTPConfig{
		Host: srv.Host(), Port: srv.Port(), User: smtpUser, Password: smtpPass,
		From: "noreply@kedo.test", TLSConfig: srv.TLSConfig(), Timeout: 5 * time.Second,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := email.NewSMTPSender(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSMTPSenderDeliversOverSTARTTLSWithAuth(t *testing.T) {
	srv := smtptest.New(t, smtptest.Options{User: smtpUser, Password: smtpPass})
	s := newSender(t, srv, nil)

	err := s.Send(context.Background(), email.Message{To: "a@x.kz", Subject: "Please sign: Договор", Body: "Open http://web.test/sign/tok\n.leading dot line\n"})
	if err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	m := msgs[0]
	if m.From != "noreply@kedo.test" || len(m.To) != 1 || m.To[0] != "a@x.kz" {
		t.Errorf("envelope = %+v", m)
	}
	if m.Subject() != "Please sign: Договор" {
		t.Errorf("subject = %q", m.Subject())
	}
	if !strings.Contains(m.Text(), "http://web.test/sign/tok") || !strings.Contains(m.Text(), ".leading dot line") {
		t.Errorf("body = %q", m.Text())
	}
	if m.Header("Message-ID") == "" || m.Header("Date") == "" {
		t.Error("missing Message-ID or Date")
	}
	if srv.PlainAuthAttempts() != 0 {
		t.Error("credentials were offered before TLS")
	}
}

func TestSMTPSenderRefusesAuthWithoutTLS(t *testing.T) {
	srv := smtptest.New(t, smtptest.Options{User: smtpUser, Password: smtpPass, NoSTARTTLS: true})
	s := newSender(t, srv, nil)

	err := s.Send(context.Background(), email.Message{To: "a@x.kz", Subject: "s", Body: "b"})
	if !errors.Is(err, email.ErrSTARTTLSRequired) {
		t.Fatalf("want ErrSTARTTLSRequired, got %v", err)
	}
	if srv.PlainAuthAttempts() != 0 || len(srv.Messages()) != 0 {
		t.Errorf("auth attempts = %d, messages = %d", srv.PlainAuthAttempts(), len(srv.Messages()))
	}
}

func TestSMTPSenderRejectsUntrustedCertificate(t *testing.T) {
	srv := smtptest.New(t, smtptest.Options{User: smtpUser, Password: smtpPass})
	s := newSender(t, srv, func(c *email.SMTPConfig) { c.TLSConfig = nil })
	if err := s.Send(context.Background(), email.Message{To: "a@x.kz", Subject: "s", Body: "b"}); err == nil {
		t.Fatal("self-signed certificate must be rejected by default")
	}
	if len(srv.Messages()) != 0 {
		t.Error("message delivered over untrusted TLS")
	}
}

func TestSMTPSenderRejectsHeaderInjection(t *testing.T) {
	srv := smtptest.New(t, smtptest.Options{User: smtpUser, Password: smtpPass})
	s := newSender(t, srv, nil)
	for _, m := range []email.Message{
		{To: "a@x.kz\r\nBcc: evil@example.com", Subject: "s", Body: "b"},
		{To: "a@x.kz", Subject: "title\r\nBcc: evil@example.com", Body: "b"},
	} {
		if err := s.Send(context.Background(), m); !errors.Is(err, email.ErrInvalidHeader) {
			t.Errorf("message %+v: %v", m, err)
		}
	}
	if len(srv.Messages()) != 0 {
		t.Error("injected message was delivered")
	}
}

func TestSMTPSenderFailuresDoNotLeakPassword(t *testing.T) {
	srv := smtptest.New(t, smtptest.Options{User: smtpUser, Password: "different"})
	s := newSender(t, srv, nil)
	err := s.Send(context.Background(), email.Message{To: "a@x.kz", Subject: "s", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "535") {
		t.Fatalf("want auth failure, got %v", err)
	}
	if strings.Contains(err.Error(), smtpPass) {
		t.Errorf("error leaks password: %v", err)
	}

	good := newSender(t, smtptest.New(t, smtptest.Options{User: smtpUser, Password: smtpPass, FailStage: "RCPT", FailReply: "550 no such user"}), nil)
	if err := good.Send(context.Background(), email.Message{To: "a@x.kz", Subject: "s", Body: "b"}); err == nil || !strings.Contains(err.Error(), "550") {
		t.Errorf("want 550, got %v", err)
	}
}

func TestSMTPSenderServerDownAndContextCancel(t *testing.T) {
	srv := smtptest.New(t, smtptest.Options{User: smtpUser, Password: smtpPass})
	s := newSender(t, srv, nil)
	srv.Close()
	if err := s.Send(context.Background(), email.Message{To: "a@x.kz", Subject: "s", Body: "b"}); err == nil {
		t.Fatal("expected dial failure")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	live := newSender(t, smtptest.New(t, smtptest.Options{User: smtpUser, Password: smtpPass}), nil)
	if err := live.Send(ctx, email.Message{To: "a@x.kz", Subject: "s", Body: "b"}); err == nil {
		t.Fatal("expected cancelled context to fail")
	}
}

func TestNewSMTPSenderValidatesConfig(t *testing.T) {
	cases := []email.SMTPConfig{
		{},
		{Host: "h", Port: 587, User: "u", Password: "p", From: "not an address"},
		{Host: "h", Port: 587, User: "u", Password: "p", From: "a@b.c\r\nBcc: x@y.z"},
		{Host: "h", Port: 0, User: "u", Password: "p", From: "a@b.c"},
	}
	for i, c := range cases {
		if _, err := email.NewSMTPSender(c); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}
