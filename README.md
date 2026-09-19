# HiveMind Backend

Go + gRPC backend for HiveMind, an IRL social/experiences platform (plans, bookings, chat, marketplace, and social discovery). Proto-first API design, PostgreSQL + PostGIS for geo-aware data, Redis for idempotency, NATS JetStream for the event bus, and MinIO for media storage.

## Tech stack

- **Language**: Go 1.26
- **API**: gRPC, defined in `proto/social/v1/*.proto`, code-generated via [buf](https://buf.build)
- **Database**: PostgreSQL with PostGIS, migrations via [golang-migrate](https://github.com/golang-migrate/migrate)
- **Cache / idempotency**: Redis
- **Event bus**: NATS JetStream (outbox pattern — domain events are written to an `outbox_events` table in the same transaction as the state change, then drained by `cmd/worker`)
- **Object storage**: MinIO (S3-compatible)
- **Payments**: Cashfree (Orders API + webhooks)
- **Email / Push**: Resend / Firebase Cloud Messaging (optional — features degrade gracefully when unconfigured)

## Project layout

```
cmd/
  api/        gRPC API server + Cashfree webhook HTTP listener
  worker/     outbox drainer + NATS event consumers
config/       env-based configuration
internal/     one package per domain (repository / service / grpc handler)
pkg/          shared infrastructure (security, grpcmiddleware, eventbus, idempotency, cashfree, email, push, oauth, analytics, observability)
proto/        gRPC service definitions (buf-managed)
gen/          generated protobuf/gRPC Go code (do not edit by hand)
migrations/   sequential SQL migrations (golang-migrate)
```

Each domain in `internal/` follows the same three-file shape:

- `repository.go` — SQL against Postgres via `pgxpool`
- `service.go` — business logic, ownership checks, validation
- `grpc.go` — the `*ServiceServer` implementation, translating between proto messages and domain types

Identity is always derived from the authenticated JWT (`grpcmiddleware.UserIDFromContext`), never trusted from client-supplied request fields.

## Getting started

1. **Start infrastructure** (Postgres, Redis, NATS, MinIO):
   ```
   make up
   ```
2. **Run migrations**:
   ```
   make migrate-up
   ```
3. **Generate proto code** (only needed after editing a `.proto` file):
   ```
   make proto
   ```
4. **Run the API server**:
   ```
   make run-api
   ```
5. **Run the background worker** (separate terminal):
   ```
   make run-worker
   ```

The gRPC server listens on `:50051` by default; the Cashfree webhook HTTP listener runs alongside it on `:8080`.

## Configuration

All configuration is via environment variables (see `config/config.go`), each with a sane local default:

| Variable | Purpose |
|---|---|
| `GRPC_PORT` | gRPC server port (default `50051`) |
| `WEBHOOK_PORT` | Cashfree webhook HTTP port (default `8080`) |
| `DATABASE_URL` | Postgres connection string |
| `REDIS_ADDR` | Redis address |
| `NATS_URL` | NATS server URL |
| `MINIO_ENDPOINT` / `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY` / `MINIO_BUCKET` | MinIO object storage |
| `JWT_SECRET` | HMAC secret for issued tokens |
| `GOOGLE_CLIENT_ID` / `APPLE_BUNDLE_ID` | OAuth sign-in (optional — disabled if unset) |
| `RESEND_API_KEY` / `EMAIL_FROM_ADDRESS` | Transactional email (optional) |
| `FIREBASE_CREDENTIALS_PATH` | Push notifications (optional) |
| `CASHFREE_CLIENT_ID` / `CASHFREE_CLIENT_SECRET` / `CASHFREE_SANDBOX` | Payment gateway (optional — orders are recorded without a payment session if unset) |

## Testing

```
make test    # go test ./...
make vet     # go vet ./...
make fmt     # gofmt -l . (lists unformatted files)
```

Most repository/service tests are integration tests that run against a live Postgres (and Redis, where idempotency is involved) — they skip automatically if `DATABASE_URL`/Redis aren't reachable, rather than mocking the database.

## Domains

The API surface is organized by domain service, each with its own proto file under `proto/social/v1/`:

**Core** — `auth`, `user`, `profile`, `discovery`, `search`, `plan`, `booking`, `payment`, `chat`, `notification`, `moderation`, `admin`

**Social** — `social` (posts/comments/likes), `community`, `connection`

**Marketplace** — `host` (host verification & payouts), `venue`, `promotion` (promoted listings), `subscription`

**AI + Smart Social** — `recommendation` (rule-based ranking, no ML), `availability` (Who's Free / Activity Buddy), `smart_groups`, plus AI icebreaker/plan-draft generation exposed via `chat`/`plan`

**Scale** — `company` (corporate/B2B team bookings), plus Travel Mode (an optional `travel_city_id` override on the recommendation/discovery RPCs) and the city-launch lifecycle (exposed via `admin`)

All rule-based/heuristic logic (recommendation ranking, content moderation, icebreakers, plan drafts) is intentionally SQL/Go-based rather than ML-backed, per the product's own non-goal against introducing heavy ML infrastructure before there's enough behavioral data to justify it.
