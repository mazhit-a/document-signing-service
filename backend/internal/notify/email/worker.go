package email

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	defaultPollInterval = time.Second
	defaultSendTimeout  = 30 * time.Second
	maxErrorLength      = 300
)

type WorkerConfig struct {
	Store        Store
	Sender       Sender
	Crypto       *Crypto
	Workers      int
	PollInterval time.Duration
	SendTimeout  time.Duration
	Logger       *slog.Logger
	Now          func() time.Time
	Jitter       func(time.Duration) time.Duration
}

type Worker struct {
	store        Store
	sender       Sender
	crypto       *Crypto
	workers      int
	pollInterval time.Duration
	sendTimeout  time.Duration
	logger       *slog.Logger
	now          func() time.Time
	jitter       func(time.Duration) time.Duration
}

func NewWorker(cfg WorkerConfig) *Worker {
	w := &Worker{
		store: cfg.Store, sender: cfg.Sender, crypto: cfg.Crypto, workers: cfg.Workers,
		pollInterval: cfg.PollInterval, sendTimeout: cfg.SendTimeout,
		logger: cfg.Logger, now: cfg.Now, jitter: cfg.Jitter,
	}
	if w.pollInterval <= 0 {
		w.pollInterval = defaultPollInterval
	}
	if w.sendTimeout <= 0 {
		w.sendTimeout = defaultSendTimeout
	}
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
			w.logger.Error("email worker iteration failed", "error", sanitizeError(err))
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

func sanitizeError(err error) string {
	msg := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, err.Error())
	if len(msg) > maxErrorLength {
		msg = msg[:maxErrorLength]
	}
	return msg
}

func (w *Worker) handle(ctx context.Context, it Item) (Outcome, error) {
	msg, permanent := w.prepare(it)
	if permanent != nil {
		w.logger.Warn("email permanently failed", "outbox", it.ID, "kind", string(it.Kind), "error", sanitizeError(permanent))
		return Outcome{Status: StatusFailed, Error: sanitizeError(permanent)}, nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, w.sendTimeout)
	sendErr := w.sender.Send(sendCtx, msg)
	cancel()
	if ctx.Err() != nil {
		return Outcome{}, ctx.Err()
	}
	if sendErr == nil {
		w.logger.Info("email sent", "outbox", it.ID, "kind", string(it.Kind), "attempt", it.Attempts+1)
		return Outcome{Status: StatusSent}, nil
	}
	if errors.Is(sendErr, ErrInvalidHeader) {
		w.logger.Warn("email permanently failed", "outbox", it.ID, "kind", string(it.Kind), "error", sanitizeError(sendErr))
		return Outcome{Status: StatusFailed, Error: sanitizeError(sendErr)}, nil
	}

	attempts := it.Attempts + 1
	msgErr := sanitizeError(sendErr)
	if attempts >= MaxAttempts {
		w.logger.Warn("email gave up", "outbox", it.ID, "kind", string(it.Kind), "attempts", attempts)
		return Outcome{Status: StatusFailed, Error: msgErr}, nil
	}
	next := w.now().Add(w.jitter(Backoff(attempts)))
	w.logger.Warn("email failed, will retry", "outbox", it.ID, "kind", string(it.Kind), "attempt", attempts, "next_attempt_at", next, "error", msgErr)
	return Outcome{Status: StatusPending, NextAttemptAt: next, Error: msgErr}, nil
}

func (w *Worker) prepare(it Item) (Message, error) {
	if len(it.Ciphertext) == 0 {
		return Message{}, errors.New("payload missing")
	}
	plain, err := w.crypto.Open(it.ID, it.Kind, it.Ciphertext)
	if err != nil {
		return Message{}, err
	}
	p, err := DecodePayload(plain)
	if err != nil {
		return Message{}, err
	}
	return Render(it.Kind, it.Recipient, p)
}
