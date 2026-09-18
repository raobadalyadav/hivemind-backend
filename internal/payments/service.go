package payments

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("payments: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateOrder(ctx context.Context, o *Order) (*Order, error) {
	if o.BookingID == "" || o.AmountMinor <= 0 {
		return nil, ErrInvalidInput
	}
	if o.Currency == "" {
		o.Currency = "INR"
	}
	return s.repo.CreateOrder(ctx, o)
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
// handler — a no-op (not an error) when no captured payment exists, since
// most cancellations in this scaffold happen before any real payment
// capture flow exists.
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
