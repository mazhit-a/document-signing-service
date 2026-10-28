// Package config parses and validates environment configuration.
// It fails fast: Load returns an error describing every problem found.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const minSecretBytes = 32

// Placeholder secrets shipped in .env.example. They are only acceptable in dev.
const (
	PlaceholderJWTSecret  = "change-me-at-least-32-bytes-long"
	PlaceholderSealSecret = "change-me-another-32-bytes-secret"
)

// Config holds all runtime settings.
type Config struct {
	AppEnv            string
	HTTPAddr          string
	DatabaseURL       string
	JWTSecret         string
	JWTTTL            time.Duration
	SealSecret        string
	StorageDir        string
	MaxUploadBytes    int64
	PublicWebURL      string
	CORSAllowedOrigin string
	WebhookWorkers    int
}

// IsDev reports whether the app runs in the dev environment.
func (c Config) IsDev() bool { return c.AppEnv == "dev" }

// Load reads configuration using getenv (usually os.Getenv).
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	str := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	required := func(key string) string {
		v := strings.TrimSpace(getenv(key))
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", key))
		}
		return v
	}

	cfg := Config{
		AppEnv:     str("APP_ENV", "dev"),
		HTTPAddr:   str("HTTP_ADDR", ":8080"),
		StorageDir: str("STORAGE_DIR", "./data/files"),
	}

	cfg.DatabaseURL = required("DATABASE_URL")
	cfg.PublicWebURL = required("PUBLIC_WEB_URL")
	cfg.CORSAllowedOrigin = required("CORS_ALLOWED_ORIGIN")

	ttl, err := time.ParseDuration(str("JWT_TTL", "24h"))
	if err != nil || ttl <= 0 {
		errs = append(errs, errors.New("JWT_TTL must be a positive duration such as 24h"))
	}
	cfg.JWTTTL = ttl

	maxUpload, err := strconv.ParseInt(str("MAX_UPLOAD_BYTES", "10485760"), 10, 64)
	if err != nil || maxUpload <= 0 {
		errs = append(errs, errors.New("MAX_UPLOAD_BYTES must be a positive integer"))
	}
	cfg.MaxUploadBytes = maxUpload

	workers, err := strconv.Atoi(str("WEBHOOK_WORKERS", "2"))
	if err != nil || workers < 0 {
		errs = append(errs, errors.New("WEBHOOK_WORKERS must be a non-negative integer"))
	}
	cfg.WebhookWorkers = workers

	switch cfg.AppEnv {
	case "dev", "staging", "prod":
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be one of dev, staging, prod (got %q)", cfg.AppEnv))
	}

	cfg.JWTSecret = secret(getenv, "JWT_SECRET", PlaceholderJWTSecret, cfg.IsDev(), &errs)
	cfg.SealSecret = secret(getenv, "SEAL_SECRET", PlaceholderSealSecret, cfg.IsDev(), &errs)

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// secret reads a secret. In dev a missing value falls back to the placeholder
// and weak values are tolerated; elsewhere it must be set, long enough and
// must not be a placeholder.
func secret(getenv func(string) string, key, placeholder string, dev bool, errs *[]error) string {
	v := getenv(key)
	if dev {
		if v == "" {
			return placeholder
		}
		return v
	}
	switch {
	case v == "":
		*errs = append(*errs, fmt.Errorf("%s is required when APP_ENV is not dev", key))
	case len(v) < minSecretBytes:
		*errs = append(*errs, fmt.Errorf("%s must be at least %d bytes", key, minSecretBytes))
	case v == placeholder || strings.HasPrefix(v, "change-me"):
		*errs = append(*errs, fmt.Errorf("%s must not be a placeholder value when APP_ENV is not dev", key))
	}
	return v
}
