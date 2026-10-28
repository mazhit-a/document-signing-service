package config

import (
	"strings"
	"testing"
)

const goodSecretA = "0123456789abcdef0123456789abcdef-A"
const goodSecretB = "0123456789abcdef0123456789abcdef-B"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func base(extra map[string]string) map[string]string {
	m := map[string]string{
		"DATABASE_URL":        "postgres://u:p@localhost/db",
		"PUBLIC_WEB_URL":      "http://localhost:5173",
		"CORS_ALLOWED_ORIGIN": "http://localhost:5173",
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestLoadSecrets(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"dev missing secrets accepted", map[string]string{"APP_ENV": "dev"}, ""},
		{"dev placeholder accepted", map[string]string{"APP_ENV": "dev", "JWT_SECRET": PlaceholderJWTSecret, "SEAL_SECRET": PlaceholderSealSecret}, ""},
		{"dev short accepted", map[string]string{"APP_ENV": "dev", "JWT_SECRET": "x", "SEAL_SECRET": "y"}, ""},
		{"prod good accepted", map[string]string{"APP_ENV": "prod", "JWT_SECRET": goodSecretA, "SEAL_SECRET": goodSecretB}, ""},
		{"prod missing jwt", map[string]string{"APP_ENV": "prod", "SEAL_SECRET": goodSecretB}, "JWT_SECRET is required"},
		{"prod missing seal", map[string]string{"APP_ENV": "prod", "JWT_SECRET": goodSecretA}, "SEAL_SECRET is required"},
		{"prod short jwt", map[string]string{"APP_ENV": "prod", "JWT_SECRET": "short", "SEAL_SECRET": goodSecretB}, "JWT_SECRET must be at least 32"},
		{"prod short seal", map[string]string{"APP_ENV": "prod", "JWT_SECRET": goodSecretA, "SEAL_SECRET": "short"}, "SEAL_SECRET must be at least 32"},
		{"prod placeholder jwt", map[string]string{"APP_ENV": "prod", "JWT_SECRET": PlaceholderJWTSecret, "SEAL_SECRET": goodSecretB}, "JWT_SECRET must not be a placeholder"},
		{"prod placeholder seal", map[string]string{"APP_ENV": "prod", "JWT_SECRET": goodSecretA, "SEAL_SECRET": PlaceholderSealSecret}, "SEAL_SECRET must not be a placeholder"},
		{"staging is not dev", map[string]string{"APP_ENV": "staging"}, "JWT_SECRET is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(base(tt.env)))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLoadDefaultsAndParsing(t *testing.T) {
	cfg, err := Load(env(base(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppEnv != "dev" || cfg.HTTPAddr != ":8080" || cfg.MaxUploadBytes != 10485760 ||
		cfg.WebhookWorkers != 2 || cfg.JWTTTL.Hours() != 24 || cfg.StorageDir != "./data/files" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"missing db", map[string]string{"DATABASE_URL": ""}, "DATABASE_URL is required"},
		{"missing cors", map[string]string{"CORS_ALLOWED_ORIGIN": ""}, "CORS_ALLOWED_ORIGIN is required"},
		{"bad ttl", map[string]string{"JWT_TTL": "soon"}, "JWT_TTL"},
		{"bad upload", map[string]string{"MAX_UPLOAD_BYTES": "-1"}, "MAX_UPLOAD_BYTES"},
		{"bad workers", map[string]string{"WEBHOOK_WORKERS": "x"}, "WEBHOOK_WORKERS"},
		{"bad trust proxy", map[string]string{"TRUST_PROXY": "maybe"}, "TRUST_PROXY"},
		{"bad env", map[string]string{"APP_ENV": "qa"}, "APP_ENV"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(base(tt.env)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

const goodOutboxKey = "0123456789abcdef0123456789abcdef-O"

func smtpEnv(extra map[string]string) map[string]string {
	m := map[string]string{
		"APP_ENV": "prod", "JWT_SECRET": goodSecretA, "SEAL_SECRET": goodSecretB,
		"SMTP_HOST": "smtp.gmail.com", "SMTP_USER": "u@gmail.com", "SMTP_PASSWORD": "app-password", "MAIL_FROM": "u@gmail.com",
		"EMAIL_OUTBOX_KEY": goodOutboxKey,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestLoadSMTP(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"complete prod config accepted", smtpEnv(nil), ""},
		{"no smtp in prod needs no key", map[string]string{"APP_ENV": "prod", "JWT_SECRET": goodSecretA, "SEAL_SECRET": goodSecretB}, ""},
		{"dev without smtp accepted", map[string]string{"APP_ENV": "dev"}, ""},
		{"host without user", smtpEnv(map[string]string{"SMTP_USER": ""}), "SMTP_USER is required when SMTP_HOST is set"},
		{"host without password", smtpEnv(map[string]string{"SMTP_PASSWORD": ""}), "SMTP_PASSWORD is required when SMTP_HOST is set"},
		{"host without from", smtpEnv(map[string]string{"MAIL_FROM": ""}), "MAIL_FROM is required when SMTP_HOST is set"},
		{"from not an address", smtpEnv(map[string]string{"MAIL_FROM": "Bob <b@x.io>"}), "MAIL_FROM must be a plain email address"},
		{"user without host", map[string]string{"APP_ENV": "dev", "SMTP_USER": "u"}, "SMTP_HOST is required when SMTP_USER is set"},
		{"bad port", smtpEnv(map[string]string{"SMTP_PORT": "70000"}), "SMTP_PORT must be a port number"},
		{"prod smtp missing key", smtpEnv(map[string]string{"EMAIL_OUTBOX_KEY": ""}), "EMAIL_OUTBOX_KEY is required"},
		{"prod smtp short key", smtpEnv(map[string]string{"EMAIL_OUTBOX_KEY": "short"}), "EMAIL_OUTBOX_KEY must be at least 32"},
		{"prod smtp placeholder key", smtpEnv(map[string]string{"EMAIL_OUTBOX_KEY": PlaceholderEmailOutboxKey}), "EMAIL_OUTBOX_KEY must not be a placeholder"},
		{"key equals jwt secret", smtpEnv(map[string]string{"EMAIL_OUTBOX_KEY": goodSecretA}), "EMAIL_OUTBOX_KEY must differ"},
		{"key equals seal secret", smtpEnv(map[string]string{"EMAIL_OUTBOX_KEY": goodSecretB}), "EMAIL_OUTBOX_KEY must differ"},
		{"short key rejected even in dev", map[string]string{"APP_ENV": "dev", "EMAIL_OUTBOX_KEY": "short"}, "EMAIL_OUTBOX_KEY must be at least 32"},
		{"negative workers", smtpEnv(map[string]string{"EMAIL_WORKERS": "-1"}), "EMAIL_WORKERS must be a non-negative integer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(base(tt.env)))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLoadSMTPDefaultsAndOutboxEnablement(t *testing.T) {
	cfg, err := Load(env(base(smtpEnv(nil))))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTPPort != 587 || cfg.EmailWorkers != 2 || !cfg.SMTPConfigured() || !cfg.EmailOutboxEnabled() || cfg.EmailOutboxKey != goodOutboxKey {
		t.Errorf("config = %+v", cfg)
	}

	dev, err := Load(env(base(map[string]string{"APP_ENV": "dev"})))
	if err != nil {
		t.Fatal(err)
	}
	if dev.SMTPConfigured() || !dev.EmailOutboxEnabled() || dev.EmailOutboxKey != PlaceholderEmailOutboxKey {
		t.Errorf("dev config = %+v", dev)
	}

	prod, err := Load(env(base(map[string]string{"APP_ENV": "prod", "JWT_SECRET": goodSecretA, "SEAL_SECRET": goodSecretB})))
	if err != nil {
		t.Fatal(err)
	}
	if prod.SMTPConfigured() || prod.EmailOutboxEnabled() {
		t.Errorf("prod without smtp must not enable the outbox: %+v", prod)
	}
}
