package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mazhit-a/mini-kedo/internal/auth"
	"github.com/mazhit-a/mini-kedo/internal/config"
	"github.com/mazhit-a/mini-kedo/internal/domain"
	"github.com/mazhit-a/mini-kedo/internal/notify"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres"
	"github.com/mazhit-a/mini-kedo/internal/service"
	"github.com/mazhit-a/mini-kedo/internal/storage/localfs"
	"github.com/mazhit-a/mini-kedo/migrations"
)

const (
	demoEmail    = "hr@demo.kz"
	demoPassword = "S3cure-pass!"
	demoName     = "HR Demo"
	demoTitle    = "Employment agreement (demo)"
	demoFileName = "employment-agreement.txt"
	demoContent  = "Demo employment agreement.\n\nThis text file exists only to demonstrate the signing flow.\nSignatures in this service are a simulation and have no legal force.\n"
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
	ids := service.UUIDGenerator{}
	hasher, err := auth.NewPasswordHasher(auth.DefaultBcryptCost)
	if err != nil {
		return err
	}
	tokens, err := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL, clock.Now)
	if err != nil {
		return err
	}
	authSvc, err := service.NewAuthService(postgres.NewUserRepository(pool), hasher, tokens, clock, ids)
	if err != nil {
		return err
	}
	files, err := localfs.New(cfg.StorageDir)
	if err != nil {
		return err
	}
	txm := postgres.NewTxManager(pool)
	docRepo := postgres.NewDocumentRepository(pool, txm)
	docSvc := service.NewDocumentService(docRepo, postgres.NewSignatureRepository(pool),
		postgres.NewAuditRepository(pool), files, txm, clock, ids, cfg.MaxUploadBytes)
	lifecycleSvc := service.NewLifecycleService(docRepo, postgres.NewSignerRepository(pool),
		postgres.NewAuditRepository(pool), txm, clock, notify.Noop{}, cfg.PublicWebURL)

	user, err := authSvc.Register(ctx, demoEmail, demoPassword, demoName)
	switch {
	case err == nil:
		slog.Info("demo owner created", "email", demoEmail)
	case errors.Is(err, domain.ErrEmailTaken):
		res, loginErr := authSvc.Login(ctx, demoEmail, demoPassword)
		if loginErr != nil {
			return fmt.Errorf("demo owner exists but password differs: %w", loginErr)
		}
		user = res.User
		slog.Info("demo owner already exists", "email", demoEmail)
	default:
		return err
	}

	page, err := docSvc.ListDocuments(ctx, service.ListDocumentsInput{OwnerID: user.ID, Limit: 1})
	if err != nil {
		return err
	}
	if len(page.Items) > 0 {
		slog.Info("demo owner already has documents, skipping sample document")
		printCredentials()
		return nil
	}

	doc, err := docSvc.CreateDocument(ctx, service.CreateDocumentInput{
		OwnerID:  user.ID,
		Title:    demoTitle,
		FileName: demoFileName,
		File:     strings.NewReader(demoContent),
		Signers: []domain.SignerInput{
			{FullName: "A. Seitov", Email: "a@demo.kz"},
			{FullName: "HR Manager", Email: "m@demo.kz"},
		},
	})
	if err != nil {
		return fmt.Errorf("create sample document: %w", err)
	}
	sent, err := lifecycleSvc.SendDocument(ctx, user.ID, doc.ID)
	if err != nil {
		return fmt.Errorf("send sample document: %w", err)
	}
	printCredentials()
	fmt.Printf("sample document %q is %s\n", sent.Document.Title, sent.Document.Status)
	for _, l := range sent.Links {
		fmt.Printf("signing link #%d: %s\n", l.Order, l.URL)
	}
	fmt.Println("later signers get their link when it is their turn (by email, or `make outbox` in dev without SMTP)")
	return nil
}

func printCredentials() {
	fmt.Printf("demo login: %s / %s\n", demoEmail, demoPassword)
}
