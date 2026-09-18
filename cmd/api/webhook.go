// Cashfree webhooks are plain HTTP POSTs (Cashfree's servers calling ours),
// not gRPC — this is the one place in cmd/api that isn't a gRPC handler,
// served on a separate port (config.WebhookPort) alongside the gRPC server.
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hivemind/backend/internal/payments"
	"github.com/hivemind/backend/pkg/cashfree"
)

const (
	webhookHandlerTimeout = 10 * time.Second
	// webhookMaxClockSkew bounds how old (or how far in the future) a
	// webhook's timestamp may be before it's rejected as a possible replay
	// — a valid signed payload captured once shouldn't be replayable
	// indefinitely. Cashfree's timestamp is milliseconds since epoch.
	webhookMaxClockSkew = 5 * time.Minute
)

func cashfreeWebhookHandler(paymentsSvc *payments.Service, cfClient *cashfree.Client, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfClient == nil {
			http.Error(w, "payment gateway not configured", http.StatusServiceUnavailable)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		signature := r.Header.Get("x-webhook-signature")
		timestamp := r.Header.Get("x-webhook-timestamp")
		if !cfClient.VerifyWebhookSignature(body, signature, timestamp) {
			logger.Warn("cashfree webhook signature verification failed")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		timestampMS, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			http.Error(w, "invalid timestamp", http.StatusBadRequest)
			return
		}
		age := time.Since(time.UnixMilli(timestampMS))
		if age < 0 {
			age = -age
		}
		if age > webhookMaxClockSkew {
			// A validly-signed payload captured once (e.g. from a proxy
			// log, a MITM before TLS, or a compromised intermediary) and
			// replayed later is otherwise indistinguishable from a live
			// Cashfree callback — MarkCaptured's UNIQUE constraint on
			// gateway_payment_id (migration 0022) is the second, DB-level
			// layer of the same defense, for a replay within this window.
			logger.Warn("cashfree webhook timestamp outside allowed skew", "age", age)
			http.Error(w, "stale webhook", http.StatusUnauthorized)
			return
		}

		event, err := cashfree.ParseWebhookEvent(body)
		if err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}

		if event.Type != cashfree.EventPaymentSuccess {
			// Other event types (PAYMENT_FAILED_WEBHOOK, refund webhooks,
			// etc.) are acknowledged but not acted on yet — TODO(phase3+):
			// handle failed-payment and refund-confirmation webhooks too.
			logger.Info("cashfree webhook received (no handler for this type)", "type", event.Type)
			w.WriteHeader(http.StatusOK)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), webhookHandlerTimeout)
		defer cancel()
		if _, err := paymentsSvc.MarkCaptured(ctx, event.OrderID, event.CFPaymentID, event.AmountMinor); err != nil {
			logger.Error("mark payment captured", "error", err, "order_id", event.OrderID)
			http.Error(w, "failed to process webhook", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
