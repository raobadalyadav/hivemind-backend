package payments

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var (
	ErrInvalidInput       = errors.New("payments: invalid input")
	ErrForbidden          = errors.New("payments: caller does not own this booking")
	ErrNotPayable         = errors.New("payments: this booking is not waiting for payment")
	ErrGatewayUnavailable = errors.New("payments: the payment gateway is not available")
	ErrPlanFullAfterPay   = errors.New("payments: the seat was taken before the payment arrived; it has been refunded")
)

// BookingPort is what payments needs from bookings (satisfied by *bookings.Service, wired in cmd/api/main.go;
// declared here so this package doesn't import bookings). The charge is computed by bookings from the plan's
// own price — a payment amount is NEVER taken from the client.
type BookingPort interface {
	GetBookingCharge(ctx context.Context, bookingID string) (ownerID string, totalMinor int64, currency, status string, err error)
	ConfirmPaidBooking(ctx context.Context, bookingID string) error
}

// ErrSeatGone is what BookingPort.ConfirmPaidBooking returns when the hold expired and the seat is taken.
// (bookings.ErrPlanFull is mapped to it by the adapter in cmd/api.)
var ErrSeatGone = errors.New("payments: seat no longer available")

// GatewayPayment is one payment attempt on an order as the gateway reports it.
type GatewayPayment struct {
	ID          string
	Status      string // SUCCESS | FAILED | PENDING | …
	AmountMinor int64
}

// GatewayClient is satisfied (through a small adapter) by *cashfree.Client — declared here so this package
// doesn't depend on Cashfree concretely. Nil is valid (CASHFREE_CLIENT_ID not configured): an order is
// recorded without a payment session and money can't move.
type GatewayClient interface {
	CreateOrder(ctx context.Context, orderID string, amountMinor int64, currency, customerID, customerPhone, customerEmail string) (cfOrderID, paymentSessionID string, err error)
	Refund(ctx context.Context, orderID, refundID string, amountMinor int64, note string) (cfRefundID string, err error)
	OrderPayments(ctx context.Context, orderID string) ([]GatewayPayment, error)
}

type Service struct {
	repo     *Repository
	gateway  GatewayClient
	bookings BookingPort
	logger   *slog.Logger
	seller   Seller
}

func NewService(repo *Repository, gateway GatewayClient, bookings BookingPort, logger *slog.Logger) *Service {
	return &Service{repo: repo, gateway: gateway, bookings: bookings, logger: logger}
}

// CreateOrder starts the payment for a booking that is waiting for it. The caller must own the booking; the
// amount is what the booking owes (price + service fee), less credits/coupon if they opted in — whatever
// amount the client sent is ignored. It returns the order and, when there is something to pay, the short-lived
// payment session the app's checkout needs. An order fully covered by credits/coupon confirms the booking on the spot.
func (s *Service) CreateOrder(ctx context.Context, o *Order, callerID, customerPhone, promoCode string, useCredits bool) (*Order, string, error) {
	if o.BookingID == "" || callerID == "" {
		return nil, "", ErrInvalidInput
	}
	ownerID, total, currency, status, err := s.bookings.GetBookingCharge(ctx, o.BookingID)
	if err != nil {
		return nil, "", err
	}
	if ownerID != callerID {
		return nil, "", ErrForbidden
	}
	if status != "payment_pending" || total <= 0 {
		return nil, "", ErrNotPayable
	}
	o.AmountMinor, o.Currency = total, currency
	if o.Currency == "" {
		o.Currency = "INR"
	}

	// Whatever they started before (an abandoned sheet, a retry) is closed first, and any credits it took go back.
	if err := s.repo.CancelUnpaidOrdersForBooking(ctx, o.BookingID); err != nil {
		return nil, "", err
	}

	if promoCode != "" {
		discount, err := s.repo.ReserveCoupon(ctx, promoCode, o.AmountMinor)
		if err != nil {
			return nil, "", err
		}
		o.AmountMinor -= discount
	}

	created, _, err := s.repo.CreateOrderWithCredits(ctx, o, callerID, useCredits)
	if err != nil {
		return nil, "", err
	}

	if created.AmountMinor <= 0 {
		// Fully covered by coupon/credits — nothing to charge: the order is settled and the seat confirmed.
		if err := s.repo.MarkOrderPaid(ctx, created.ID); err != nil {
			return nil, "", err
		}
		created.Status = "paid"
		if err := s.bookings.ConfirmPaidBooking(ctx, o.BookingID); err != nil {
			return nil, "", err
		}
		return created, "", nil
	}

	if s.gateway == nil {
		s.logger.Warn("payment gateway not configured, order created without a payment session", "order_id", created.ID)
		return created, "", ErrGatewayUnavailable
	}

	userID, email, err := s.repo.GetBookingUser(ctx, o.BookingID)
	if err != nil {
		return nil, "", err
	}
	cfOrderID, sessionID, err := s.gateway.CreateOrder(ctx, created.ID, created.AmountMinor, created.Currency, userID, customerPhone, email)
	if err != nil {
		s.logger.Error("cashfree create order failed", "error", err, "order_id", created.ID)
		_ = s.repo.CancelUnpaidOrdersForBooking(ctx, o.BookingID) // give back credits; the person can try again
		return nil, "", ErrGatewayUnavailable
	}
	if err := s.repo.SetGatewayOrderID(ctx, created.ID, cfOrderID); err != nil {
		s.logger.Error("store gateway order id", "error", err, "order_id", created.ID)
	}
	created.GatewayOrderID = cfOrderID
	return created, sessionID, nil
}

// MarkCaptured is called by the Cashfree webhook (after it verified the signature) and by VerifyOrder: it records
// the payment and confirms the booking it paid for. Replays are harmless. If the seat was lost while the hold
// had expired, the payment is refunded in full.
func (s *Service) MarkCaptured(ctx context.Context, orderID, gatewayPaymentID string, amountMinor int64) (*Payment, error) {
	order, err := s.repo.FindOrderByCashfreeOrderID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	payment, err := s.repo.MarkCaptured(ctx, order.ID, gatewayPaymentID, amountMinor, order.Currency)
	if err != nil {
		return nil, err
	}
	if s.bookings != nil {
		if err := s.bookings.ConfirmPaidBooking(ctx, order.BookingID); err != nil {
			if errors.Is(err, ErrSeatGone) {
				s.logger.Warn("seat taken before payment arrived — refunding", "booking_id", order.BookingID, "payment_id", payment.ID)
				if _, rerr := s.refund(ctx, payment.ID, payment.AmountMinor, "seat no longer available"); rerr != nil {
					return payment, rerr
				}
				return payment, ErrPlanFullAfterPay
			}
			return payment, err
		}
	}
	return payment, nil
}

// VerifyOrder asks the gateway what became of an order the caller placed and settles it — the app calls it right
// after the checkout sheet closes, so the booking is confirmed even if the webhook is slow or unreachable.
func (s *Service) VerifyOrder(ctx context.Context, orderID, callerID string) (*Order, error) {
	if orderID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	order, err := s.repo.FindOrderByCashfreeOrderID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	ownerID, _, _, _, err := s.bookings.GetBookingCharge(ctx, order.BookingID)
	if err != nil {
		return nil, err
	}
	if ownerID != callerID {
		return nil, ErrPaymentNotFound // don't reveal other people's orders
	}
	if order.Status != "paid" && s.gateway != nil {
		if err := s.settleFromGateway(ctx, order.ID); err != nil {
			return nil, err
		}
	} else if order.Status == "paid" {
		// already captured: make sure the booking followed (it is idempotent)
		if err := s.bookings.ConfirmPaidBooking(ctx, order.BookingID); err != nil && !errors.Is(err, ErrSeatGone) {
			return nil, err
		}
	}
	return s.repo.FindOrderByCashfreeOrderID(ctx, order.ID)
}

// settleFromGateway asks the gateway for the order's payments and captures the first successful one.
func (s *Service) settleFromGateway(ctx context.Context, orderID string) error {
	list, err := s.gateway.OrderPayments(ctx, orderID)
	if err != nil {
		return err
	}
	for _, gp := range list {
		if gp.Status == "SUCCESS" {
			if _, err := s.MarkCaptured(ctx, orderID, gp.ID, gp.AmountMinor); err != nil && !errors.Is(err, ErrPlanFullAfterPay) {
				return err
			}
			break
		}
	}
	return nil
}

// ReconcileOrders is the safety net for a payment that succeeded while nobody was listening (webhook lost, app
// closed before VerifyOrder): it asks the gateway about recent unsettled orders and captures what was paid. A
// payment that lands after its seat was released is refunded by MarkCaptured. Worker job — must run before the
// hold-expiry job so a paid seat is confirmed rather than released.
func (s *Service) ReconcileOrders(ctx context.Context, _ time.Time) (int, error) {
	if s.gateway == nil {
		return 0, nil
	}
	ids, err := s.repo.UnsettledOrderIDs(ctx, 50)
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, id := range ids {
		if err := s.settleFromGateway(ctx, id); err != nil {
			continue // the gateway hiccuped on one order; the next run tries again
		}
		if o, err := s.repo.FindOrderByCashfreeOrderID(ctx, id); err == nil && o.Status == "paid" {
			settled++
		}
	}
	return settled, nil
}

func (s *Service) GetMyCreditBalance(ctx context.Context, userID string) (int64, error) {
	if userID == "" {
		return 0, ErrInvalidInput
	}
	return s.repo.GetCreditBalance(ctx, userID)
}

// GetPayment returns a payment to its owner (or staff). Anyone else gets "not found", so ids can't be probed.
func (s *Service) GetPayment(ctx context.Context, id, callerID string, staff bool) (*Payment, error) {
	if id == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	if !staff {
		owner, err := s.repo.PaymentOwner(ctx, id)
		if err != nil {
			return nil, err
		}
		if owner != callerID {
			return nil, ErrPaymentNotFound
		}
	}
	return s.repo.GetPayment(ctx, id)
}

// RefundPayment is the staff refund (RPC is admin-gated).
func (s *Service) RefundPayment(ctx context.Context, paymentID string, amountMinor int64, reason string) (*Refund, error) {
	if paymentID == "" || amountMinor <= 0 {
		return nil, ErrInvalidInput
	}
	return s.refund(ctx, paymentID, amountMinor, reason)
}

// refund records the attempt, asks the gateway to return the money, then settles the record. A gateway
// failure marks the attempt failed and is returned (the worker retries with a fresh attempt); the money already
// returned by earlier attempts is counted, so the total can never exceed what was paid.
func (s *Service) refund(ctx context.Context, paymentID string, amountMinor int64, reason string) (*Refund, error) {
	if s.gateway == nil {
		return nil, ErrGatewayUnavailable
	}
	r, orderID, err := s.repo.BeginRefund(ctx, paymentID, amountMinor, reason)
	if err != nil {
		return nil, err
	}
	cfRefundID, err := s.gateway.Refund(ctx, orderID, r.ID, amountMinor, reason)
	if err != nil {
		s.logger.Error("gateway refund failed", "error", err, "refund_id", r.ID, "payment_id", paymentID)
		_ = s.repo.FailRefund(ctx, r.ID)
		return nil, err
	}
	if err := s.repo.CompleteRefund(ctx, r.ID, cfRefundID, amountMinor); err != nil {
		return nil, err
	}
	r.Status = "processed"
	return r, nil
}

// RefundBookingIfCaptured is called by the worker's BOOKING_CANCELLED handler: it returns percent% of what the
// booking paid, minus anything already refunded. A no-op (not an error) when nothing was paid or nothing is owed.
func (s *Service) RefundBookingIfCaptured(ctx context.Context, bookingID, reason string, percent int) error {
	if percent <= 0 {
		return nil
	}
	payment, err := s.repo.FindCapturedPaymentForBooking(ctx, bookingID)
	if err != nil {
		if err == ErrPaymentNotFound {
			return nil
		}
		return err
	}
	target := payment.AmountMinor * int64(min(percent, 100)) / 100
	already, err := s.repo.RefundedTotal(ctx, payment.ID)
	if err != nil {
		return err
	}
	if owed := target - already; owed > 0 {
		_, err = s.refund(ctx, payment.ID, owed, reason)
		return err
	}
	return nil
}

// ReleaseAbandonedOrders closes unpaid orders of bookings that no longer wait for payment (the hold expired or
// was cancelled) and gives their credits back (worker job).
func (s *Service) ReleaseAbandonedOrders(ctx context.Context, _ time.Time) (int, error) {
	return s.repo.ReleaseAbandonedOrders(ctx)
}

// RecordRefundResult applies the gateway's final word on a refund (Cashfree's refund webhook). A refund that
// failed or was cancelled no longer counts as returned, so the worker's next attempt refunds the difference;
// staff are told through the log line and the failed row.
func (s *Service) RecordRefundResult(ctx context.Context, refundID, status string) error {
	if refundID == "" {
		return nil // not one of ours
	}
	switch status {
	case "SUCCESS":
		return s.repo.SetRefundStatus(ctx, refundID, "processed")
	case "FAILED", "CANCELLED":
		s.logger.Error("refund did not go through at the gateway", "refund_id", refundID, "status", status)
		return s.repo.SetRefundStatus(ctx, refundID, "failed")
	}
	return nil
}
