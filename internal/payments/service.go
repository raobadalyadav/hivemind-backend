package payments

import (
	"context"
	"errors"
	"log/slog"
)

var (
	ErrInvalidInput = errors.New("payments: invalid input")
	ErrForbidden    = errors.New("payments: caller does not own this booking")
)

// BookingOwnerChecker is satisfied by *bookings.Service (wired in
// cmd/api/main.go) — CreateOrder uses it to verify the caller owns the
// booking they're paying for. Without this, any authenticated user could
// create a real Cashfree payment order against someone else's booking.
type BookingOwnerChecker interface {
	GetBookingOwnerID(ctx context.Context, bookingID string) (string, error)
}

// GatewayClient is satisfied by *cashfree.Client (wired in cmd/api/main.go)
// — declared here, not imported from pkg/cashfree, so this package doesn't
// depend on Cashfree concretely. Nil is valid (CASHFREE_CLIENT_ID not
// configured): the order is still recorded, just without a real payment
// session — same graceful-degradation pattern as the OAuth verifiers and
// email/push senders. Webhook signature verification/parsing is Cashfree-
// specific enough that it stays in cmd/api's webhook HTTP handler (the
// composition root) rather than being abstracted through this interface —
// see MarkCaptured below, which is what that handler calls into.
type GatewayClient interface {
	CreateOrder(ctx context.Context, orderID string, amountMinor int64, currency, customerID, customerPhone, customerEmail string) (cfOrderID, paymentSessionID string, err error)
}

type Service struct {
	repo         *Repository
	gateway      GatewayClient
	bookingOwner BookingOwnerChecker
	logger       *slog.Logger
}

func NewService(repo *Repository, gateway GatewayClient, bookingOwner BookingOwnerChecker, logger *slog.Logger) *Service {
	return &Service{repo: repo, gateway: gateway, bookingOwner: bookingOwner, logger: logger}
}

// CreateOrder requires the caller to own the booking being paid for, then
// always records the order row — a failed/unconfigured gateway call
// degrades to "order exists, no payment session yet" rather than failing
// the whole booking flow. PaymentSessionID is returned separately (not
// stored on Order) since it's short-lived and only needed once,
// immediately, by the mobile client's Checkout SDK.
func (s *Service) CreateOrder(ctx context.Context, o *Order, callerID, customerPhone string) (*Order, string, error) {
	if o.BookingID == "" || o.AmountMinor <= 0 || callerID == "" {
		return nil, "", ErrInvalidInput
	}
	ownerID, err := s.bookingOwner.GetBookingOwnerID(ctx, o.BookingID)
	if err != nil {
		return nil, "", err
	}
	if ownerID != callerID {
		return nil, "", ErrForbidden
	}
	if o.Currency == "" {
		o.Currency = "INR"
	}
	created, err := s.repo.CreateOrder(ctx, o)
	if err != nil {
		return nil, "", err
	}

	if s.gateway == nil {
		s.logger.Warn("payment gateway not configured, order created without a payment session", "order_id", created.ID)
		return created, "", nil
	}

	userID, email, err := s.repo.GetBookingUser(ctx, o.BookingID)
	if err != nil {
		s.logger.Error("resolve booking user for payment order", "error", err, "order_id", created.ID)
		return created, "", nil
	}

	cfOrderID, sessionID, err := s.gateway.CreateOrder(ctx, created.ID, created.AmountMinor, created.Currency, userID, customerPhone, email)
	if err != nil {
		s.logger.Error("cashfree create order failed", "error", err, "order_id", created.ID)
		return created, "", nil
	}
	if err := s.repo.SetGatewayOrderID(ctx, created.ID, cfOrderID); err != nil {
		s.logger.Error("store gateway order id", "error", err, "order_id", created.ID)
	}
	created.GatewayOrderID = cfOrderID
	return created, sessionID, nil
}

// MarkCaptured is called by cmd/api's Cashfree webhook HTTP handler after
// it has independently verified the webhook signature — not part of
// GatewayClient since it's driven by an inbound webhook, not an outbound
// gateway call.
func (s *Service) MarkCaptured(ctx context.Context, cashfreeOrderID, gatewayPaymentID string, amountMinor int64) (*Payment, error) {
	order, err := s.repo.FindOrderByCashfreeOrderID(ctx, cashfreeOrderID)
	if err != nil {
		return nil, err
	}
	return s.repo.MarkCaptured(ctx, order.ID, gatewayPaymentID, amountMinor, order.Currency)
}

func (s *Service) GetPayment(ctx context.Context, id string) (*Payment, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.GetPayment(ctx, id)
}

func (s *Service) RefundPayment(ctx context.Context, paymentID string, amountMinor int64, reason string) (*Refund, error) {
	if paymentID == "" || amountMinor <= 0 {
		return nil, ErrInvalidInput
	}
	return s.repo.CreateRefund(ctx, paymentID, amountMinor, reason)
}

// RefundBookingIfCaptured is called by cmd/worker's BOOKING_CANCELLED
// handler — a no-op (not an error) when no captured payment exists.
func (s *Service) RefundBookingIfCaptured(ctx context.Context, bookingID, reason string) error {
	payment, err := s.repo.FindCapturedPaymentForBooking(ctx, bookingID)
	if err != nil {
		if err == ErrPaymentNotFound {
			return nil
		}
		return err
	}
	_, err = s.repo.CreateRefund(ctx, payment.ID, payment.AmountMinor, reason)
	return err
}
