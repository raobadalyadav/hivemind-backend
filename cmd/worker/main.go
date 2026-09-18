// cmd/worker drains the outbox table to NATS (PRD §19: "Transaction → Outbox
// row → Publisher → NATS JetStream → Worker → Push/Email/Analytics/Search")
// and consumes domain events for downstream side effects. Handlers here are
// deliberately minimal — real fan-out to push/email/analytics per PRD §19's
// consumer table is a TODO(phase1+) per event type.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hivemind/backend/config"
	"github.com/hivemind/backend/pkg/eventbus"
	"github.com/hivemind/backend/pkg/observability"
)

const outboxPollInterval = 2 * time.Second

// consumedEvents lists the PRD §19 domain events this worker handles.
// Each currently only logs — TODO(phase1): wire real consumers (chat
// membership, push, refunds, search invalidation, etc.) per the PRD table.
var consumedEvents = []string{
	"BOOKING_CONFIRMED",
	"BOOKING_CANCELLED",
	"PLAN_CANCELLED",
	"PLAN_COMPLETED",
	"USER_REPORTED",
	"PAYMENT_CAPTURED",
	"PAYOUT_PROCESSED",
}

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

	publisher, err := eventbus.Connect(cfg.NATSURL)
	if err != nil {
		logger.Error("connect nats", "error", err)
		os.Exit(1)
	}

	for _, eventType := range consumedEvents {
		et := eventType
		err := publisher.Consume(ctx, "worker-"+et, et, func(msg jetstream.Msg) error {
			logger.Info("event received", "type", et, "data", string(msg.Data()))
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
