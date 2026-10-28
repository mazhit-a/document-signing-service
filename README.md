# Mini-KEDO

Sequential document signing with integrity verification. An owner uploads a file, defines an ordered list of signers and sends it. Each signer opens a personal one-time link and approves or rejects in strict order. The service stores a SHA-256 of the file, seals every signature with an HMAC, and records every state change in a hash-chained audit log, so a single `verify` call proves whether the file, the signatures or the history were altered. Go + PostgreSQL backend, React + TypeScript frontend.

> **Signatures are a simulation.** They are not legally significant ЭЦП, do not use X.509, eGov mobile or NCALayer, and carry no legal force. The project demonstrates the architecture, data integrity and API design around signing, not a certified signing system.

## Architecture

```
            browser (React, TanStack Query)
                        |
                        v
              nginx :8081  (static SPA, proxies /api, sets CSP)
                        |
                        v
   +--------------------- api (Go, net/http) ----------------------+
   |  transport/http   handlers, middleware, error mapping          |
   |        |                                                       |
   |  service          use cases, injected Clock / ID generator     |
   |        |                                                       |
   |  domain           entities, state machine, domain errors       |
   |  integrity        sha256 stream, HMAC seal, audit hash chain   |
   |  repository/pg    pgx, TxManager, SELECT ... FOR UPDATE        |
   |  storage/localfs  files under random keys                      |
   |  webhook worker   polls outbox, signs and delivers             |
   +-------------------------------+--------------------------------+
                                   |
                                   v
                       PostgreSQL 16 (documents, signers,
                       signatures, audit_events, webhook outbox)
```

Layering is transport -> service -> domain; SQL lives only in the repository layer. Signing mutations run in one transaction with `SELECT ... FOR UPDATE` on the document row, and the audit event and webhook outbox rows are written in that same transaction.

## Run it

```bash
git clone <repo-url> && cd document-signing-service
cp .env.example .env
make up
```

The web UI and API are served at <http://localhost:8081> (override with `WEB_PORT`). A one-shot `seed` service creates a demo owner and a sample document in progress:

- email `hr@demo.kz`, password `S3cure-pass!`
- the seed output prints the first signer's link: `docker compose logs seed`; later signers' links are emailed when SMTP is configured, otherwise `make outbox` prints them in dev (see [Email](#email-notifications))

`make down` stops the stack; `docker compose down -v` also resets the data.

Compose defaults to `APP_ENV=dev` with placeholder secrets. With any other `APP_ENV` the API refuses to start unless `JWT_SECRET` and `SEAL_SECRET` are set, at least 32 bytes and not the placeholders.

### Development

```bash
make dev-api    # Postgres from compose, API on :8080
make dev-web    # Vite on :5173, proxies /api to :8080
make migrate    # apply SQL migrations
make test       # go test -race ./... and vitest
make lint       # go vet, staticcheck, eslint, tsc --noEmit
```

Go 1.26+ and Node 22 are expected. Tests use a real Postgres through `TEST_DATABASE_URL` (the Makefile sets it from `DATABASE_URL`). CI (`.github/workflows/ci.yml`) runs the same checks plus Docker builds.

## Happy-path walkthrough

Needs `curl`, `jq`, `make` and Go (for `make outbox`) and a running stack (`make up`). The walkthrough registers a new owner so it does not collide with the seeded `hr@demo.kz`. Re-running it requires a different email.

```bash
API=http://localhost:8081/api/v1
printf 'Employment contract.\nDemo only, no legal force.\n' > contract.txt

# 1. Register + login
curl -s -X POST $API/auth/register -H 'content-type: application/json' \
  -d '{"email":"owner@demo.kz","password":"S3cure-pass!","full_name":"Owner Demo"}'
TOKEN=$(curl -s -X POST $API/auth/login -H 'content-type: application/json' \
  -d '{"email":"owner@demo.kz","password":"S3cure-pass!"}' | jq -r .access_token)

# 2. Create a document with two signers
DOC=$(curl -s -X POST $API/documents -H "authorization: Bearer $TOKEN" \
  -F title='Employment contract' -F file=@contract.txt \
  -F 'signers=[{"full_name":"A. Seitov","email":"a@demo.kz"},{"full_name":"HR Manager","email":"m@demo.kz"}]' | jq -r .id)

# 3. Send: only signer #1 gets a link, shown once. Signer #2 has no token yet.
LINKS=$(curl -s -X POST $API/documents/$DOC/send -H "authorization: Bearer $TOKEN")
TOKEN_1=$(echo "$LINKS" | jq -r '.signing_links[0].url | split("/") | last')

curl -s -X POST $API/sign/not-a-real-token/approve | jq   # 404 not_found
curl -s -X POST $API/sign/$TOKEN_1/approve | jq           # 200, signer 1 signed
curl -s -X POST $API/sign/$TOKEN_1/approve | jq           # 200, same result (idempotent)

# 3b. Approving signer 1 issued a fresh token for signer 2 and queued an email.
# Without SMTP (dev) read the link from the encrypted outbox; with SMTP it arrives by email.
TOKEN_2=$(make -s outbox | grep m@demo.kz | awk '{print $NF}' | awk -F/ '{print $NF}')
curl -s -X POST $API/sign/$TOKEN_2/approve | jq           # 200, document completed

# 4. Verify integrity and read the audit log
curl -s $API/documents/$DOC/verify -H "authorization: Bearer $TOKEN" | jq
curl -s $API/documents/$DOC/events -H "authorization: Bearer $TOKEN" | jq '.items[].type'
```

Reject instead of approve with `curl -s -X POST $API/sign/$TOKEN_1/reject -H 'content-type: application/json' -d '{"reason":"Wrong salary amount"}'`; the document becomes `rejected` and the flow stops.

## API summary

| Method and path | Auth | Purpose |
|---|---|---|
| `POST /auth/register`, `POST /auth/login`, `GET /me` | - / - / owner | Account and JWT |
| `POST /documents`, `GET /documents` | owner | Upload (multipart) and list with keyset pagination |
| `GET /documents/{id}`, `/file`, `/events`, `/verify` | owner | Detail, download, audit log, integrity report |
| `POST /documents/{id}/send`, `/cancel` | owner | Send (returns signer #1's link only), cancel |
| `GET /sign/{token}`, `/file`; `POST /sign/{token}/approve`, `/reject` | token | Public signing flow |
| `POST/GET/DELETE /webhooks` | owner | Webhook endpoints |
| `GET /healthz`, `GET /readyz` | - | Liveness and readiness |

All errors use `{ "error": { "code", "message", "fields" } }`. A wrong turn is `409 not_your_turn`, a closed document `409 document_closed`, an unknown token `404 not_found`. Another owner's document is a `404`, not a `403`.

## Design decisions and trade-offs

**HMAC seal instead of asymmetric signatures.** Each signature stores a canonical payload `v1|document_id|sha256|signer_id|order|signed_at` and `HMAC-SHA256(SEAL_SECRET, payload)`. This proves that this server produced the record and that it was not altered afterwards. It does not give non-repudiation by the signer: anyone holding `SEAL_SECRET` could forge a seal, and the signer holds no key. Per-signer Ed25519 keys would fix that at the cost of key management and enrolment. Timestamps are truncated to microseconds so a Postgres round trip does not break verification.

**Tamper-evident audit log.** Every event stores `prev_hash` and `hash = SHA256(prev_hash || canonical(type, actor, data, created_at))` per document, starting from 64 zeros. Database triggers reject `UPDATE`/`DELETE` on audit rows and on the document file hash. `verify` walks the chain and reports the first broken event. A hash chain detects edits; someone with full database and secret access could still rewrite the whole chain, so for stronger guarantees the head hash should be anchored externally.

**Signing links (token at turn).** Tokens are 32 bytes from `crypto/rand`; only `sha256(token)` is stored, and logs redact them. A token is issued only when it becomes the signer's turn: `send` issues signer #1's token and returns it once, and approving signer k issues a fresh token for signer k+1 in the same transaction. Signers who are still `waiting` have a `NULL` `token_hash`, so their link cannot exist yet and resolves to `404`. Tokens stop working once the signer decided or the document is terminal. The public endpoints are rate limited.

**Email notifications use an encrypted outbox.** When a signer's turn starts, a `signer_turn` row is inserted into `email_outbox` in the signing transaction; completion and rejection insert `owner_completed` / `owner_rejected` rows for the owner (no email on cancel). A worker pool claims rows with `FOR UPDATE SKIP LOCKED` and sends over SMTP, so a mail outage never rolls back or blocks a signature: the row simply stays `pending` and is retried (10s, 30s, 2m, 10m, 1h; six attempts, then `failed`; permanent 5xx replies are retried the same way). The raw link has to be stored until it is sent, so the row payload is AES-256-GCM ciphertext (random 12-byte nonce, AAD = row id + kind) under a key derived with HKDF-SHA256 from `EMAIL_OUTBOX_KEY`, which is separate from the JWT and seal secrets. The ciphertext is set to `NULL` in the same `UPDATE` that marks the row `sent` or terminally `failed`. Residual risk: until a row is sent, ciphertext sits in the database, so someone with both database access and `EMAIL_OUTBOX_KEY` can read pending links; the window is bounded by the retry schedule (about 73 minutes of cumulative backoff before a row fails). Delivery is at-least-once: a crash between the SMTP server accepting a message and the database update can send it twice. Known limitation: because later signers' raw tokens are never shown to the owner, a signer whose email permanently fails cannot be reached. A reissue endpoint is out of scope for now.

**Concurrency.** Approve and reject lock the document row (`FOR UPDATE`), re-check whose turn it is, and write signature and audit event in one transaction. A parallel-approve test asserts exactly one signature under `-race`. Repeated approves are idempotent.

**Webhooks use a transactional outbox.** The delivery row is inserted in the same transaction as the audit event, so a rolled-back signing never emits a webhook and a committed one never loses it. Workers claim rows with `FOR UPDATE SKIP LOCKED`, sign the body with the endpoint secret (HMAC-SHA256), and retry with backoff (10s, 30s, 2m, 10m, 1h; six attempts). Delivery is at-least-once, so receivers should dedupe on `X-Kedo-Delivery`. `make webhook-echo` runs a local receiver that verifies signatures.

**JWT in localStorage.** The SPA keeps the owner JWT in `localStorage` for simplicity. That makes it readable by any script running on the page, so an XSS bug would leak it. Mitigations here: nginx sends a strict CSP, `nosniff` and `X-Frame-Options`, React escapes output, and tokens have a TTL. An `HttpOnly` `SameSite` cookie plus CSRF protection would be the safer production choice. The public signing page needs no JWT; the link is the credential.

**Uploads.** `MaxBytesReader`, extension allowlist (`.pdf`, `.docx`, `.txt`) plus sniffed content type, random storage keys, and the file is streamed through SHA-256 while it is written, never buffered whole.

**Immutability and state machine.** Signatures, files and audit rows are immutable after creation. Statuses `completed`, `rejected` and `cancelled` are terminal, and all transitions go through one state machine in `domain`.

## Configuration

See `.env.example`. Main variables: `APP_ENV`, `DATABASE_URL`, `JWT_SECRET`, `JWT_TTL`, `SEAL_SECRET`, `STORAGE_DIR`, `MAX_UPLOAD_BYTES`, `PUBLIC_WEB_URL`, `CORS_ALLOWED_ORIGIN`, `WEBHOOK_WORKERS`, `TRUST_PROXY`, plus the email settings below.

### Email notifications

| Variable | Default | Meaning |
|---|---|---|
| `SMTP_HOST` | empty | Empty disables delivery (no worker starts). Gmail: `smtp.gmail.com` |
| `SMTP_PORT` | `587` | STARTTLS port; the sender refuses to send credentials without STARTTLS |
| `SMTP_USER` | - | Required when `SMTP_HOST` is set |
| `SMTP_PASSWORD` | - | Required when `SMTP_HOST` is set (Gmail app password) |
| `MAIL_FROM` | - | Required when `SMTP_HOST` is set; a plain address such as `you@gmail.com` |
| `EMAIL_OUTBOX_KEY` | dev placeholder | At least 32 bytes, distinct from `JWT_SECRET` and `SEAL_SECRET`; required and non-placeholder when `SMTP_HOST` is set and `APP_ENV` is not `dev` |
| `EMAIL_WORKERS` | `2` | Concurrent delivery workers |

Partial SMTP settings (for example a host without user, password or from) make the API fail at startup.

**Gmail app password.** Turn on 2-Step Verification for the Google account, open <https://myaccount.google.com/apppasswords>, create an app password named for this service, and put the 16 characters in `SMTP_PASSWORD` (`SMTP_USER` and `MAIL_FROM` are the Gmail address). Set it in `.env`, which is not committed. Gmail rewrites the sender to the authenticated account.

**Without SMTP.** The log notifier is used, which never logs links, and no email worker runs. In `APP_ENV=dev` the outbox still records each turn, so `make outbox` decrypts and prints the pending links of later signers; outside dev without SMTP the outbox is disabled and later signers' links cannot be obtained. The seeded demo uses `@demo.kz` addresses, so with SMTP configured those emails go nowhere useful.

## Non-goals

No real ЭЦП, X.509 or eGov integration, no SMS and no mail provider other than plain SMTP (signer #1's link is returned to the owner, later links are emailed), no multi-tenant organisations, no document editing or server-side PDF rendering.
