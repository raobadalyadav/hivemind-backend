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

## Local development

```bash
cp .env.example .env     # once; every variable is optional, defaults match docker-compose
make dev                 # docker compose up + migrations + API on :50051 (make run-worker in a second terminal)
make dev-token           # user id + 24h access token for the mobile app's "Developer sign-in"
make lan-ip              # the API_HOST a phone on the same Wi-Fi should use
```

`make` loads `.env` automatically. `cmd/devtoken` refuses to run unless `APP_ENV` is empty or `dev`.

## Wishlist, ratings and Me-tab stats

`PlanService.SavePlan / UnsavePlan / ListSavedPlans` (table `plan_saves`); every plan read is decorated in batch (no N+1) with `saved_by_me` and the **host rating** (average/count over reviews of all the host's plans). `ProfileService.GetMyStats` returns real counts (attended, upcoming, connections, communities). `ListConnections` filters by `status` + `direction` in SQL, `ListMyChats` has `ALL` / `GROUPS` tabs, `ListCommunities` has `only_mine` and `is_member`, and `VenueService.ListVenues` powers the plan creator's location search.

## Communities, settings and chat paging

`Community` carries `is_member` and `join_pending` (the caller's own pending non-invite request), so a client can render Join / Request sent / Joined without local state; joining after a declined approval request returns `FAILED_PRECONDITION` with a readable message. `NotificationService.GetPreferences` reads what `UpdatePreferences` saves (push/email). `GetPlanParticipantsResponse.hidden_by_me` and `Profile.show_in_participant_previews` (owner only) expose the privacy toggle's real state. `BookingSummary.reviewed` says whether the caller already rated a booking. `ChatService.ListMessages` pages: the response's `page.next_page_token` is passed back as `page.page_token` to fetch older messages (50 per page, newest first).

## Notifications and richer profiles

Social events write **in-app notifications synchronously** through `notifications.Emit` (`internal/notifications/emit.go`) — no worker/NATS needed. One place enforces the rules: never to yourself, never across a block, never to a non-active account, never in a category the recipient muted (`muted_categories`: `social`, `stories`, `profile_views`, `messages`, `connections`), and a repeat with the same `dedupe_key` collapses while the earlier one is unread. Emitters: post like/comment, story view/like (`MarkStoryViewed`, `LikeStory`, `ListStoryViewers` reuse the one story-visibility predicate), profile view (`RecordProfileView`, one per viewer/day, off when either side sets `hide_profile_views`), connection request/accepted, DM messages (collapsed until read; `MarkRead` on the chat clears them), community join request/approved/declined/member joined, plan attendee and review for hosts. `ListNotifications` pages (30, cursor), `MarkNotificationsRead`, `GetUnreadCount`. Profiles: `ListPosts` pages with the same `visibleSQL` as every list (own profile = everything, others = what the viewer may see), `GetUserStats` (posts/connections/mutuals/shared communities/host rating/member since; NotFound when blocked), `GetConnectionStatus`, `SearchPlans.host_id`. Real phone push (FCM) still needs a Firebase project and client setup.

## Hardening

`APP_ENV=production` makes `config.Validate()` refuse to start with dev secrets/URLs (see `.env.example`). gRPC calls pass through: per-IP limit on token-less calls (sign-in, refresh, recovery) → authentication → refusal of suspended/deleted accounts (30 s cache) → per-user limit. `RefundPayment` is staff-only; `GetPayment` and `GetCase` return only the owner's/reporter's (or staff's) records and otherwise "not found"; `GetUser` (which carries the email) is self-or-staff. Search `ILIKE` escapes `%`/`_`. `DeleteAccount` erases personal data and frees the e-mail/sign-in identity in one transaction (money, review and moderation records stay) and is refused while the user has upcoming bookings, live hosted plans or owns a community.

## Paid bookings

A plan with a price books in two steps. `CreateBooking` → `payment_pending` (seat held for 15 min: `PaymentHold`; holds count against capacity, the `expire_pending_bookings` job releases them, `release_abandoned_orders` returns credits). `CreateOrder` ignores any client amount: the order is price + service fee (5%, ≤ ₹49) from the plan, less credits/coupon if opted in; a fully covered order confirms at once. Payment is confirmed by the Cashfree webhook **or** `PaymentService.VerifyOrder` (asks Cashfree; the app calls it after checkout so nothing depends on a public webhook) — both go through `payments.MarkCaptured`, which is idempotent, confirms the booking, and refunds in full if the seat was lost while the hold had lapsed. Cancelling: `RefundPercent` = 100% when cancelled ≥ 24 h before the start or by the host, else 0; `BOOKING_CANCELLED` carries it and the **worker** refunds through Cashfree (`Refund` attempts are recorded, retried, never exceed the payment). Run `cmd/worker` in every environment — refunds, reminders and outbox events depend on it. Failed events are retried with back-off and dead-lettered after 6 tries. Sandbox test cards: Cashfree docs; `CASHFREE_SANDBOX=true` and the app's `CASHFREE_PRODUCTION=false` must match.

## Demo data

`go run ./cmd/seed -keeper <your-user-uuid>` fills a **local dev** database with realistic data in your city: 24 people (photos, bios, some blue-ticked), 32 plans over the next four weeks with venues and bookings, posts with comments/likes, live stories, communities, friends / pending requests / incoming waves for the keeper, three DMs and two plan chats, and six external events. It also gives the keeper's city a real name and drops unused test cities/categories. Everything it creates uses the e-mail domain `@seed.hivemind.local`, so re-running replaces only its own rows; the keeper's own rows are never modified. Refuses to run unless `APP_ENV` is empty or `dev`. Photos are hot-linked (randomuser.me, picsum.photos), so the phone needs internet.

`DEV_EMAIL=you@example.com mobile/tool/dev_env.sh` then signs the app in as that existing user.

Note: `audit_logs` and the credit ledgers are append-only by design, so users with rows there can't be hard-deleted; clean them up by suspending instead.

## Media uploads

`POST /v1/media` (multipart `file`, optional `poster`/`width`/`height`/`duration_ms`, `Authorization: <access token>`)
stores a photo or video in MinIO/S3 and returns `{id, url, thumb_url, kind, width, height, duration_ms}`; stories, posts,
chat messages, profile photos and plan covers then reference it by **id** (`media_id`/`media_ids`/`cover_media_id`).
`GET /media/{key}` serves files (Range supported). Photos are decoded and re-encoded as JPEG (EXIF/GPS stripped, ≤ 2048 px,
480 px thumbnail); videos are stored as sent (≤ `MEDIA_MAX_VIDEO_MB`). Uploads nothing references after 24 h are deleted by
the worker's `media_gc` job. `MEDIA_PUBLIC_BASE_URL` must be reachable from phones (`make run-api` defaults it to your LAN IP).

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
| `PASS_SECRET` | HMAC secret for signed digital-pass (QR) payloads (defaults to `JWT_SECRET` + `:pass`) |
| `EMERGENCY_NUMBER` | Local emergency number shown in the Safety Center and SOS response (default `112`) |
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

**Plan lifecycle & access** — waitlist with timed seat offers, digital pass + host `ScanPass` check-in, no-show marking, 24h/3h/1h reminders, plan types (open / approval / invite-only; public / private / community; premium via entitlement), community types (public / private / approval / paid) and recurring plans (`FREQ=DAILY|WEEKLY|MONTHLY`, expanded into real plan rows). Time-based work runs as jobs in `cmd/worker/jobs.go` (1-minute ticker; each job is a single claiming SQL statement, so it is safe on several worker replicas).

**Profile & people** — photos, gender/education/hobbies, interest catalog (5–15 interests), social intent + personality quiz (feeds people recommendations and smart-group balancing), "who's going" cards with privacy filtering, Meet Again groups, Memories.

**Meet, waves & verification** — `MeetService`: a same-city swipe deck (shared interests/communities first; blocked, connected and recently passed people never appear), `Swipe` pass/wave/super with daily limits (300/50/5), mutual wave = accepted connection + DM room, 30-minute profile boosts (one free per week, then ₹19 from wallet credits). `VerificationService`: live-selfie blue tick — random pose challenge, selfie upload, admin approve/reject (`ListVerificationRequests` / `ReviewVerification`, audited); the selfie is detached and garbage-collected after review. Chat inbox: `ListMyChats` (Primary = friend DMs + upcoming plan chats, General = the rest), `MarkRead`, `OpenDirectChat` (connections only). Feed `FOR_YOU` scope and `ListUpcomingPlans` power Home and Events.

**Chat, feed & stories** — typed chat messages (image, voice, location, announcement), polls and pinned messages; a feed with connections/community visibility, save and share-link; 24-hour stories with optional archive.

**Growth & safety** — referral codes (₹100 credit each side, append-only ledger), Safety Center, emergency contact and SOS (records the alert and emails the contact when an email provider is configured — it does **not** dispatch emergency services, and says so), blocked-users list, and admin-curated external events with interest and event group chats.

Deliberately not built: ticket resale/ecosystem, brand partnerships, ML recommendation, third-party event sync, server-side QR image rendering (the server returns the signed payload; the client renders it), SMS to emergency contacts, community chat rooms.

All rule-based/heuristic logic (recommendation ranking, content moderation, icebreakers, plan drafts) is intentionally SQL/Go-based rather than ML-backed, per the product's own non-goal against introducing heavy ML infrastructure before there's enough behavioral data to justify it.
