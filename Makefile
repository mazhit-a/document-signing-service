-include .env
export

POSTGRES_PORT ?= 5432
DATABASE_URL ?= postgres://kedo:kedo@localhost:$(POSTGRES_PORT)/kedo?sslmode=disable
PUBLIC_WEB_URL ?= http://localhost:5173
CORS_ALLOWED_ORIGIN ?= http://localhost:5173

export POSTGRES_PORT DATABASE_URL PUBLIC_WEB_URL CORS_ALLOWED_ORIGIN

.PHONY: up down migrate migrate-down dev-api dev-web webhook-echo test-backend test-web

up:
	docker compose up -d --wait postgres

down:
	docker compose down

migrate:
	cd backend && go run ./cmd/migrate up

migrate-down:
	cd backend && go run ./cmd/migrate down

dev-api:
	cd backend && go run ./cmd/api

test-backend:
	cd backend && TEST_DATABASE_URL=$(DATABASE_URL) go test -race ./...

webhook-echo:
	cd backend && go run ./cmd/webhook-echo -addr :9000 -secret "$(WEBHOOK_SECRET)"

dev-web:
	cd frontend && npm run dev

test-web:
	cd frontend && npm run lint && npm run typecheck && npm test
