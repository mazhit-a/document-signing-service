package email

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxReasonRunes = 200

var (
	ErrInvalidHeader = errors.New("email header contains forbidden characters")
	ErrInvalidKind   = errors.New("unknown email kind")
)

func oneLine(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
}

func truncate(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "..."
}

func Render(kind Kind, recipient string, p Payload) (Message, error) {
	title := oneLine(p.DocumentTitle)
	var subject string
	var b strings.Builder
	switch kind {
	case KindSignerTurn:
		subject = "Please sign: " + title
		fmt.Fprintf(&b, "Hello %s,\n\n", oneLine(p.SignerName))
		fmt.Fprintf(&b, "%s (%s) asked you to review and sign \"%s\".\n", oneLine(p.OwnerName), oneLine(p.OwnerEmail), title)
		fmt.Fprintf(&b, "You are signer %d of %d.\n\n", p.Position, p.Total)
		b.WriteString("Open this personal link to review and sign the document:\n")
		b.WriteString(oneLine(p.Link))
		b.WriteString("\n\nDo not forward this link: it is tied to you.\n")
	case KindOwnerCompleted:
		subject = "Completed: " + title
		fmt.Fprintf(&b, "Hello %s,\n\n", oneLine(p.OwnerName))
		fmt.Fprintf(&b, "All %d signers have approved \"%s\". The document is completed.\n", p.Total, title)
	case KindOwnerRejected:
		subject = "Rejected: " + title
		fmt.Fprintf(&b, "Hello %s,\n\n", oneLine(p.OwnerName))
		fmt.Fprintf(&b, "%s rejected \"%s\".\n\n", oneLine(p.SignerEmail), title)
		fmt.Fprintf(&b, "Reason: %s\n", truncate(oneLine(p.Reason), maxReasonRunes))
	default:
		return Message{}, ErrInvalidKind
	}
	b.WriteString("\nSignatures in this service are a simulation and have no legal force.\n")
	return Message{To: recipient, Subject: subject, Body: b.String()}, nil
}

func checkHeaderValue(v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return ErrInvalidHeader
	}
	return nil
}

func checkAddress(v string) (string, error) {
	if err := checkHeaderValue(v); err != nil {
		return "", err
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v {
		return "", ErrInvalidHeader
	}
	return addr.Address, nil
}

func newMessageID(from string, rnd func([]byte) (int, error)) (string, error) {
	domain := "localhost"
	if i := strings.LastIndex(from, "@"); i >= 0 && i+1 < len(from) {
		domain = from[i+1:]
	}
	raw := make([]byte, 16)
	if _, err := rnd(raw); err != nil {
		return "", fmt.Errorf("message id: %w", err)
	}
	return "<" + hex.EncodeToString(raw) + "@" + domain + ">", nil
}

func BuildMIME(from string, msg Message, now time.Time) ([]byte, error) {
	if _, err := checkAddress(from); err != nil {
		return nil, err
	}
	if _, err := checkAddress(msg.To); err != nil {
		return nil, err
	}
	if err := checkHeaderValue(msg.Subject); err != nil {
		return nil, err
	}
	id, err := newMessageID(from, rand.Read)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	header := func(k, v string) { buf.WriteString(k + ": " + v + "\r\n") }
	header("From", from)
	header("To", msg.To)
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", id)
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=UTF-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(strings.ReplaceAll(msg.Body, "\n", "\r\n"))); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	return buf.Bytes(), nil
}
