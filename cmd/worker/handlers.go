package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hivemind/backend/internal/bookings"
	"github.com/hivemind/backend/internal/chat"
	"github.com/hivemind/backend/internal/notifications"
	"github.com/hivemind/backend/internal/payments"
	"github.com/hivemind/backend/internal/plans"
	"github.com/hivemind/backend/internal/stories"
	"github.com/hivemind/backend/pkg/analytics"
	"github.com/hivemind/backend/pkg/eventbus"
)

// deps holds the services worker event handlers call into — real business
// logic, not just logging, for the events plans/bookings actually emit.
type deps struct {
	chatSvc          *chat.Service
	notificationsSvc *notifications.Service
	paymentsSvc      *payments.Service
	bookingsSvc      *bookings.Service
	plansSvc         *plans.Service
	storiesSvc       *stories.Service
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
	// Nobody should be offered a seat on a plan that no longer exists.
	if err := d.bookingsSvc.ExpireWaitlistForPlan(ctx, p.PlanID); err != nil {
		return err
	}
	return d.bookingsSvc.CancelAllForPlan(ctx, p.PlanID, "plan cancelled: "+p.Reason)
}

// handleNotifyUser delivers the generic NOTIFY_USER event (waitlist offers,
// join requests, invites, referral rewards, ...) through the notification
// service. Delivery failures are recorded on the notification row, not
// returned, so a dead push token doesn't redeliver the event forever.
func (d *deps) handleNotifyUser(ctx context.Context, data []byte) error {
	var p eventbus.NotifyUserPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	channel := p.Channel
	if channel == "" {
		channel = "push"
	}
	_, err := d.notificationsSvc.SendNotification(ctx, &notifications.Notification{
		UserID: p.UserID, Channel: channel, Title: p.Title, Body: p.Body, DeepLink: p.DeepLink,
	})
	return err
}

func (d *deps) handleBookingNoShow(ctx context.Context, data []byte) error {
	var p bookingConfirmedPayload // same {booking_id, plan_id, user_id} shape
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if err := d.analyticsRec.Record(ctx, p.UserID, "booking_no_show", map[string]any{"booking_id": p.BookingID, "plan_id": p.PlanID}); err != nil {
		d.logger.Error("record booking_no_show event", "error", err)
	}
	return nil
}

// logOnly handles events without a consumer yet (PLAN_COMPLETED is now
// produced by the complete_plans job but nothing consumes it beyond a log;
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
