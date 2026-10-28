package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mazhit-a/mini-kedo/internal/auth"
	"github.com/mazhit-a/mini-kedo/internal/config"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/notify/webhook"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/storage/localfs"
	transport "github.com/mazhit-a/mini-kedo/internal/transport/http"
	"github.com/mazhit-a/mini-kedo/migrations"
)

const (
	readTimeout       = 30 * time.Second
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second

	multipartOverhead = 1 << 20

	loginBurst  = 10
	loginRefill = 6 * time.Second
	signBurst   = 60
	signRefill  = time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := postgres.MigrateUp(cfg.DatabaseURL, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	clock := service.SystemClock{}
	hasher, err := auth.NewPasswordHasher(auth.DefaultBcryptCost)
	if err != nil {
		return err
	}
	tokens, err := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL, clock.Now)
	if err != nil {
		return err
	}
	authSvc, err := service.NewAuthService(postgres.NewUserRepository(pool), hasher, tokens, clock, service.UUIDGenerator{})
	if err != nil {
		return err
	}

	files, err := localfs.New(cfg.StorageDir)
	if err != nil {
		return err
	}
	txm := postgres.NewTxManager(pool)
	docSvc := service.NewDocumentService(
		postgres.NewDocumentRepository(pool, txm),
		postgres.NewSignatureRepository(pool),
		postgres.NewAuditRepository(pool),
		files, txm, clock, service.UUIDGenerator{}, cfg.MaxUploadBytes)

	lifecycleSvc := service.NewLifecycleService(
		postgres.NewDocumentRepository(pool, txm),
		postgres.NewSignerRepository(pool),
		postgres.NewAuditRepository(pool),
		txm, clock, notify.NewLogNotifier(logger), cfg.PublicWebURL)

	webhookRepo := postgres.NewWebhookRepository(pool, txm)
	webhookSvc := service.NewWebhookService(webhookRepo, clock, service.UUIDGenerator{})

	signingSvc := service.NewSigningService(
		postgres.NewDocumentRepository(pool, txm),
		postgres.NewSignerRepository(pool),
		postgres.NewSignatureRepository(pool),
		postgres.NewAuditRepository(pool),
		files, txm, clock, service.UUIDGenerator{}, notify.NewLogNotifier(logger), []byte(cfg.SealSecret)).WithOutbox(webhookRepo)

	verifySvc := service.NewVerifyService(
		postgres.NewDocumentRepository(pool, txm),
		postgres.NewSignatureRepository(pool),
		postgres.NewAuditRepository(pool),
		files, txm, clock, []byte(cfg.SealSecret))

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: transport.NewRouter(transport.Options{
			Logger:            logger,
			CORSAllowedOrigin: cfg.CORSAllowedOrigin,
			MaxBodyBytes:      cfg.MaxUploadBytes + multipartOverhead,
			Auth:              authSvc,
			Tokens:            tokens,
			Documents:         docSvc,
			Lifecycle:         lifecycleSvc,
			Signing:           signingSvc,
			Verify:            verifySvc,
			Webhooks:          webhookSvc,
			ReadyChecks: map[string]transport.ReadyCheck{
				"database": pool.Ping,
				"storage":  files.Check,
			},
			LoginLimiter: transport.NewLimiter(loginBurst, loginRefill, nil),
			SignLimiter:  transport.NewLimiter(signBurst, signRefill, nil),
			TrustProxy:   cfg.TrustProxy,
		}),
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	workersDone := make(chan struct{})
	go func() {
		defer close(workersDone)
		webhook.NewWorker(webhook.Config{
			Store: webhookRepo, Workers: cfg.WebhookWorkers, Logger: logger,
		}).Run(workerCtx)
	}()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	stopWorkers()
	if err := srv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	select {
	case <-workersDone:
	case <-shutCtx.Done():
		return fmt.Errorf("shutdown: webhook workers did not stop: %w", shutCtx.Err())
	}
	logger.Info("stopped")
	return nil
}
