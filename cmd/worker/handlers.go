package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hivemind/backend/internal/bookings"
	"github.com/hivemind/backend/internal/chat"
	"github.com/hivemind/backend/internal/notifications"
	"github.com/hivemind/backend/internal/payments"
	"github.com/hivemind/backend/pkg/analytics"
)

// deps holds the services worker event handlers call into — real business
// logic, not just logging, for the events plans/bookings actually emit.
type deps struct {
	chatSvc          *chat.Service
	notificationsSvc *notifications.Service
	paymentsSvc      *payments.Service
	bookingsSvc      *bookings.Service
	analyticsRec     *analytics.Recorder
	logger           *slog.Logger
}

type bookingConfirmedPayload struct {
	BookingID string `json:"booking_id"`
	PlanID    string `json:"plan_id"`
	UserID    string `json:"user_id"`
}

// handleBookingConfirmed wires the chat auto-membership PRD §31 requires and
// sends a real booking-confirmation notification — see PRD §19's consumer
// table ("BOOKING_CONFIRMED → Chat membership, push, analytics, recommendation").
func (d *deps) handleBookingConfirmed(ctx context.Context, data []byte) error {
	var p bookingConfirmedPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}

	if err := d.chatSvc.EnsureMembership(ctx, p.PlanID, p.UserID); err != nil {
		return err
	}

	if err := d.analyticsRec.Record(ctx, p.UserID, "booking_confirmed", map[string]any{"booking_id": p.BookingID, "plan_id": p.PlanID}); err != nil {
		d.logger.Error("record booking_confirmed event", "error", err)
	}

	_, err := d.notificationsSvc.SendNotification(ctx, &notifications.Notification{
		UserID:   p.UserID,
		Channel:  "push",
		Title:    "Booking confirmed",
		Body:     "You're in! Check the plan chat for details.",
		DeepLink: "hivemind://bookings/" + p.BookingID,
	})
	return err
}

type bookingCancelledPayload struct {
	BookingID string `json:"booking_id"`
	PlanID    string `json:"plan_id"`
	UserID    string `json:"user_id"`
	Reason    string `json:"reason"`
}

// handleBookingCancelled evaluates a refund (no-op if nothing was captured)
// and notifies the user — PRD §19: "BOOKING_CANCELLED → Refund worker,
// notifications, seat inventory" (seat inventory is already decremented
// transactionally in bookings.Repository.Cancel, not here).
func (d *deps) handleBookingCancelled(ctx context.Context, data []byte) error {
	var p bookingCancelledPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}

	if err := d.paymentsSvc.RefundBookingIfCaptured(ctx, p.BookingID, "booking cancelled: "+p.Reason); err != nil {
		return err
	}

	if err := d.analyticsRec.Record(ctx, p.UserID, "booking_cancelled", map[string]any{"booking_id": p.BookingID, "plan_id": p.PlanID}); err != nil {
		d.logger.Error("record booking_cancelled event", "error", err)
	}

	_, err := d.notificationsSvc.SendNotification(ctx, &notifications.Notification{
		UserID:   p.UserID,
		Channel:  "push",
		Title:    "Booking cancelled",
		Body:     "Your booking was cancelled. Refund evaluated automatically.",
		DeepLink: "hivemind://bookings/" + p.BookingID,
	})
	return err
}

type planCancelledPayload struct {
	PlanID string `json:"plan_id"`
	Reason string `json:"reason"`
}

// handlePlanCancelled fans out to per-booking cancellation — each of those
// writes its own BOOKING_CANCELLED event, so refund/notification logic
// isn't duplicated here (PRD §19: "PLAN_CANCELLED → Refunds, notifications,
// search invalidation"; search invalidation is implicit since SearchPlans
// already filters on status='published').
func (d *deps) handlePlanCancelled(ctx context.Context, data []byte) error {
	var p planCancelledPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	return d.bookingsSvc.CancelAllForPlan(ctx, p.PlanID, "plan cancelled: "+p.Reason)
}

// logOnly handles events nothing in this codebase emits yet (PLAN_COMPLETED,
// USER_REPORTED, PAYMENT_CAPTURED, PAYOUT_PROCESSED) — no real gateway
// webhook, review-prompt scheduler, or moderation-risk-engine trigger exists
// to produce them. Logging honestly reflects that rather than pretending a
// consumer implementation exists for events with no producer.
func (d *deps) logOnly(eventType string) func(ctx context.Context, data []byte) error {
	return func(ctx context.Context, data []byte) error {
		d.logger.Info("event received (no consumer wired yet)", "type", eventType, "data", string(data))
		return nil
	}
}
