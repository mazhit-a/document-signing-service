package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const defaultSMTPTimeout = 20 * time.Second

var ErrSTARTTLSRequired = errors.New("smtp server does not offer STARTTLS; refusing to send credentials")

type SMTPConfig struct {
	Host      string
	Port      int
	User      string
	Password  string
	From      string
	Timeout   time.Duration
	TLSConfig *tls.Config
	Now       func() time.Time
}

type SMTPSender struct {
	cfg SMTPConfig
}

func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" || cfg.Port <= 0 || cfg.User == "" || cfg.Password == "" || cfg.From == "" {
		return nil, errors.New("smtp sender requires host, port, user, password and from")
	}
	if _, err := checkAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("smtp from address: %w", err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultSMTPTimeout
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &SMTPSender{cfg: cfg}, nil
}

func (s *SMTPSender) redact(err error) error {
	if err == nil {
		return nil
	}
	return redactedError{msg: strings.ReplaceAll(err.Error(), s.cfg.Password, "[redacted]"), err: err}
}

type redactedError struct {
	msg string
	err error
}

func (e redactedError) Error() string { return e.msg }

func (e redactedError) Unwrap() error { return e.err }

func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	raw, err := BuildMIME(s.cfg.From, msg, s.cfg.Now())
	if err != nil {
		return err
	}
	if err := s.deliver(ctx, msg.To, raw); err != nil {
		return s.redact(err)
	}
	return nil
}

func (s *SMTPSender) deliver(ctx context.Context, to string, raw []byte) error {
	deadline := time.Now().Add(s.cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp deadline: %w", err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); !ok {
		return ErrSTARTTLSRequired
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.cfg.TLSConfig != nil {
		tlsCfg = s.cfg.TLSConfig.Clone()
	}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = s.cfg.Host
	}
	if err := c.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}
	if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	_ = c.Quit()
	return nil
}
