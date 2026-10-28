package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/notify/webhook"
)

const maxBodyBytes = 1 << 20

type receiver struct {
	secret string
	logger *slog.Logger
	mu     sync.Mutex
	seen   map[string]bool
}

func (rc *receiver) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	delivery := r.Header.Get(webhook.HeaderDelivery)
	signature := r.Header.Get(webhook.HeaderSignature)

	verified := "skipped (no -secret)"
	if rc.secret != "" {
		if !webhook.Verify(rc.secret, body, signature) {
			rc.logger.Warn("signature mismatch", "delivery", delivery)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		verified = "ok"
	}

	rc.mu.Lock()
	duplicate := rc.seen[delivery]
	rc.seen[delivery] = true
	rc.mu.Unlock()

	rc.logger.Info("webhook received",
		"event", r.Header.Get(webhook.HeaderEvent),
		"delivery", delivery,
		"timestamp", r.Header.Get(webhook.HeaderTimestamp),
		"signature", verified,
		"duplicate", duplicate,
		"body", string(body))
	w.WriteHeader(http.StatusNoContent)
}

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	secret := flag.String("secret", os.Getenv("WEBHOOK_SECRET"), "endpoint secret used to verify X-Kedo-Signature (optional)")
	flag.Parse()

	rc := &receiver{secret: *secret, logger: slog.New(slog.NewTextHandler(os.Stdout, nil)), seen: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /", rc.handle)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	rc.logger.Info("webhook-echo listening", "addr", *addr, "verify", *secret != "")
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
