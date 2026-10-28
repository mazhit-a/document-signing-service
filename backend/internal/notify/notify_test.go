package notify_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/notify"
)

func TestLogNotifierNeverLogsTokenOrEmail(t *testing.T) {
	var buf bytes.Buffer
	n := notify.NewLogNotifier(slog.New(slog.NewJSONHandler(&buf, nil)))
	err := n.SigningRequested(context.Background(), notify.SigningRequest{
		DocumentID: "doc-1", DocumentTitle: "T", SignerID: "s-1", SignerName: "A", SignerEmail: "a@x.kz",
		Order: 1, URL: "http://localhost/sign/SECRET-TOKEN",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "SECRET-TOKEN") || strings.Contains(out, "a@x.kz") {
		t.Errorf("sensitive data in log: %s", out)
	}
	if !strings.Contains(out, "doc-1") || !strings.Contains(out, "s-1") {
		t.Errorf("expected identifiers in log: %s", out)
	}
}
