package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	defaultPollInterval = time.Second
	defaultHTTPTimeout  = 10 * time.Second
	maxErrorLength      = 500
	maxDrainBytes       = 64 << 10
)

type Config struct {
	Store        Store
	Workers      int
	PollInterval time.Duration
	HTTPTimeout  time.Duration
	Client       *http.Client
	Logger       *slog.Logger
	Now          func() time.Time
	Jitter       func(time.Duration) time.Duration
}

type Worker struct {
	store        Store
	workers      int
	pollInterval time.Duration
	httpTimeout  time.Duration
	client       *http.Client
	logger       *slog.Logger
	now          func() time.Time
	jitter       func(time.Duration) time.Duration
}

func NewWorker(cfg Config) *Worker {
	w := &Worker{
		store: cfg.Store, workers: cfg.Workers, pollInterval: cfg.PollInterval, httpTimeout: cfg.HTTPTimeout,
		client: cfg.Client, logger: cfg.Logger, now: cfg.Now, jitter: cfg.Jitter,
	}
	if w.pollInterval <= 0 {
		w.pollInterval = defaultPollInterval
	}
	if w.httpTimeout <= 0 {
		w.httpTimeout = defaultHTTPTimeout
	}
	if w.client == nil {
		w.client = &http.Client{}
	}
	redirectless := *w.client
	redirectless.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	w.client = &redirectless
	if w.logger == nil {
		w.logger = slog.Default()
	}
	if w.now == nil {
		w.now = func() time.Time { return time.Now().UTC() }
	}
	if w.jitter == nil {
		w.jitter = RandomJitter
	}
	return w
}

func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx)
		}()
	}
	wg.Wait()
}

func (w *Worker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := w.store.ProcessNext(ctx, w.now(), w.handle)
		if err != nil && ctx.Err() == nil {
			w.logger.Error("webhook worker iteration failed", "error", err.Error())
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(w.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type payload struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	DocumentID string         `json:"document_id"`
	CreatedAt  string         `json:"created_at"`
	Data       map[string]any `json:"data"`
}

func (w *Worker) handle(ctx context.Context, d Delivery) (Outcome, error) {
	data := d.EventData
	if data == nil {
		data = map[string]any{}
	}
	body, err := json.Marshal(payload{
		ID: d.IdempotencyKey, Type: d.EventType, DocumentID: d.DocumentID,
		CreatedAt: d.EventAt.UTC().Format(time.RFC3339Nano), Data: data,
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("encode webhook payload: %w", err)
	}

	sendErr := w.send(ctx, d, body)
	if ctx.Err() != nil {
		return Outcome{}, ctx.Err()
	}
	now := w.now()
	if sendErr == nil {
		w.logger.Info("webhook delivered", "delivery", d.IdempotencyKey, "event", d.EventType, "attempt", d.Attempts+1)
		return Outcome{Status: StatusDelivered}, nil
	}

	msg := sendErr.Error()
	if len(msg) > maxErrorLength {
		msg = msg[:maxErrorLength]
	}
	attempts := d.Attempts + 1
	if attempts >= MaxAttempts {
		w.logger.Warn("webhook delivery gave up", "delivery", d.IdempotencyKey, "event", d.EventType, "attempts", attempts)
		return Outcome{Status: StatusFailed, Error: msg}, nil
	}
	next := now.Add(w.jitter(Backoff(attempts)))
	w.logger.Warn("webhook delivery failed, will retry", "delivery", d.IdempotencyKey, "event", d.EventType, "attempt", attempts, "next_attempt_at", next)
	return Outcome{Status: StatusPending, NextAttemptAt: next, Error: msg}, nil
}

func (w *Worker) send(ctx context.Context, d Delivery, body []byte) error {
	reqCtx, cancel := context.WithTimeout(ctx, w.httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, d.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mini-kedo-webhooks/1")
	req.Header.Set(HeaderEvent, d.EventType)
	req.Header.Set(HeaderDelivery, d.IdempotencyKey)
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(w.now().Unix(), 10))
	req.Header.Set(HeaderSignature, Sign(d.Secret, body))

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return errors.New("unexpected status " + strconv.Itoa(resp.StatusCode))
	}
	return nil
}
