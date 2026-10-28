-include .env
export

POSTGRES_PORT ?= 5432
DATABASE_URL ?= postgres://kedo:kedo@localhost:$(POSTGRES_PORT)/kedo?sslmode=disable
PUBLIC_WEB_URL ?= http://localhost:5173
CORS_ALLOWED_ORIGIN ?= http://localhost:5173
STATICCHECK ?= go run honnef.co/go/tools/cmd/staticcheck@v0.8.2

export POSTGRES_PORT DATABASE_URL PUBLIC_WEB_URL CORS_ALLOWED_ORIGIN

.PHONY: up down db migrate migrate-down seed dev-api dev-web webhook-echo outbox test test-backend test-web lint lint-backend lint-web

up:
	docker compose up --build -d --wait web

down:
	docker compose down

db:
	docker compose up -d --wait postgres

migrate: db
	cd backend && go run ./cmd/migrate up

migrate-down:
	cd backend && go run ./cmd/migrate down

seed: db
	cd backend && go run ./cmd/seed

dev-api: db
	cd backend && go run ./cmd/api

dev-web:
	cd frontend && npm run dev

webhook-echo:
	cd backend && go run ./cmd/webhook-echo -addr :9000 -secret "$(WEBHOOK_SECRET)"

outbox:
	cd backend && go run ./cmd/outbox-peek

test: db test-backend test-web

test-backend:
	cd backend && TEST_DATABASE_URL=$(DATABASE_URL) go test -race ./...

test-web:
	cd frontend && npm test

lint: lint-backend lint-web

lint-backend:
	cd backend && go vet ./... && $(STATICCHECK) ./...

lint-web:
	cd frontend && npm run lint && npm run typecheck
