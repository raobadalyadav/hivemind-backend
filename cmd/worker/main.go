// cmd/worker drains the outbox table to NATS (PRD §19: "Transaction → Outbox
// row → Publisher → NATS JetStream → Worker → Push/Email/Analytics/Search")
// and consumes domain events for downstream side effects. See handlers.go
// for what each event actually does.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"

	"github.com/hivemind/backend/config"
	"github.com/hivemind/backend/internal/bookings"
	"github.com/hivemind/backend/internal/chat"
	"github.com/hivemind/backend/internal/moderation"
	"github.com/hivemind/backend/internal/notifications"
	"github.com/hivemind/backend/internal/payments"
	"github.com/hivemind/backend/pkg/analytics"
	"github.com/hivemind/backend/pkg/email"
	"github.com/hivemind/backend/pkg/eventbus"
	"github.com/hivemind/backend/pkg/idempotency"
	"github.com/hivemind/backend/pkg/observability"
	"github.com/hivemind/backend/pkg/push"
)

const outboxPollInterval = 2 * time.Second

func main() {
	logger := observability.NewLogger()
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	publisher, err := eventbus.Connect(cfg.NATSURL)
	if err != nil {
		logger.Error("connect nats", "error", err)
		os.Exit(1)
	}

	// Declared as the interface type — see cmd/api/main.go's identical note
	// on why a nil *email.Client/*push.Client must not be assigned directly.
	var emailSender notifications.EmailSender
	if cfg.ResendAPIKey != "" {
		emailSender = email.NewClient(cfg.ResendAPIKey, cfg.EmailFromAddress)
	}
	var pushSender notifications.PushSender
	if cfg.FirebaseCredentialsPath != "" {
		p, err := push.NewClient(ctx, cfg.FirebaseCredentialsPath)
		if err != nil {
			logger.Error("firebase push client setup failed, push notifications disabled", "error", err)
		} else {
			pushSender = p
		}
	}

	// moderationSvc is needed transitively by chat.Service's ReportSubmitter
	// interface, even though the worker never calls chat.ReportMessage
	// itself — see internal/chat/service.go.
	d := &deps{
		chatSvc:          chat.NewService(chat.NewRepository(pool), moderation.NewService(moderation.NewRepository(pool)), moderation.NewScreener(), chat.NewTemplateIcebreaker()),
		notificationsSvc: notifications.NewService(notifications.NewRepository(pool), emailSender, pushSender, logger),
		// nil gateway/bookingOwner: the worker only calls
		// RefundBookingIfCaptured, never CreateOrder — those params exist
		// for the gRPC-facing Service constructed in cmd/api.
		paymentsSvc:  payments.NewService(payments.NewRepository(pool), nil, nil, logger),
		bookingsSvc:  bookings.NewService(bookings.NewRepository(pool), idempotency.NewGuard(rdb)),
		analyticsRec: analytics.NewRecorder(pool),
		logger:       logger,
	}

	handlers := map[string]func(context.Context, []byte) error{
		"BOOKING_CONFIRMED": d.handleBookingConfirmed,
		"BOOKING_CANCELLED": d.handleBookingCancelled,
		"PLAN_CANCELLED":    d.handlePlanCancelled,
		"PLAN_COMPLETED":    d.logOnly("PLAN_COMPLETED"),
		"USER_REPORTED":     d.logOnly("USER_REPORTED"),
		"PAYMENT_CAPTURED":  d.logOnly("PAYMENT_CAPTURED"),
		"PAYOUT_PROCESSED":  d.logOnly("PAYOUT_PROCESSED"),
	}

	for eventType, handle := range handlers {
		et, h := eventType, handle
		err := publisher.Consume(ctx, "worker-"+et, et, func(msg jetstream.Msg) error {
			if err := h(ctx, msg.Data()); err != nil {
				logger.Error("event handler failed", "type", et, "error", err)
				return err
			}
			return nil
		})
		if err != nil {
			logger.Error("register consumer", "event", et, "error", err)
			os.Exit(1)
		}
	}

	logger.Info("worker started", "poll_interval", outboxPollInterval.String())
	ticker := time.NewTicker(outboxPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker exited")
			return
		case <-ticker.C:
			if err := eventbus.Drain(ctx, pool, publisher); err != nil {
				logger.Error("outbox drain", "error", err)
			}
		}
	}
}
