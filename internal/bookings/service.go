package bookings

import (
	"context"
	"errors"

	"github.com/hivemind/backend/pkg/idempotency"
)

var ErrInvalidInput = errors.New("bookings: invalid input")

// Service fee formula per PRD §25/§26: ~5% of price, capped at ₹49.
const (
	serviceFeeRate     = 0.05
	maxServiceFeeMinor = 4900 // ₹49.00 in paise
)

type Quote struct {
	PriceMinor      int64
	ServiceFeeMinor int64
	TotalMinor      int64
	Currency        string
	Eligible        bool
}

type Service struct {
	repo  *Repository
	guard *idempotency.Guard
}

func NewService(repo *Repository, guard *idempotency.Guard) *Service {
	return &Service{repo: repo, guard: guard}
}

func (s *Service) CreateBooking(ctx context.Context, b *Booking) (*Booking, error) {
	if b.PlanID == "" || b.UserID == "" {
		return nil, ErrInvalidInput
	}
	if err := s.guard.Reserve(ctx, b.IdempotencyKey); err != nil {
		return nil, err
	}
	return s.repo.Create(ctx, b)
}

// CreateBookingForPlan satisfies plans.BookingCreator — the adapter
// JoinPlan calls into, so plans doesn't duplicate capacity-enforcement
// logic that already lives here.
func (s *Service) CreateBookingForPlan(ctx context.Context, planID, userID, idempotencyKey string) (string, error) {
	b, err := s.CreateBooking(ctx, &Booking{PlanID: planID, UserID: userID, IdempotencyKey: idempotencyKey})
	if err != nil {
		return "", err
	}
	return b.ID, nil
}

// CancelBookingForPlan satisfies plans.BookingCanceller — LeavePlan only
// carries (plan_id, user_id), so it looks up the active booking first.
func (s *Service) CancelBookingForPlan(ctx context.Context, planID, userID string) error {
	bookingID, err := s.repo.FindActiveBookingID(ctx, planID, userID)
	if err != nil {
		return err
	}
	_, err = s.CancelBooking(ctx, bookingID, "left plan")
	return err
}

// CancelAllForPlan is called by cmd/worker's PLAN_CANCELLED handler to fan
// out per-booking cancellation. Each CancelBooking call writes its own
// BOOKING_CANCELLED outbox event, so refund evaluation and notifications
// happen through that same handler — no duplicated logic here.
func (s *Service) CancelAllForPlan(ctx context.Context, planID, reason string) error {
	ids, err := s.repo.ListConfirmedBookingIDsForPlan(ctx, planID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.CancelBooking(ctx, id, reason); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GetBooking(ctx context.Context, id string) (*Booking, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) QuoteBooking(ctx context.Context, planID, userID string) (*Quote, error) {
	if planID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	pricing, err := s.repo.GetPlanPricing(ctx, planID)
	if err != nil {
		return nil, err
	}

	fee := int64(float64(pricing.PriceMinor) * serviceFeeRate)
	if fee > maxServiceFeeMinor {
		fee = maxServiceFeeMinor
	}

	eligible := pricing.Status == "published" && pricing.ConfirmedCount < pricing.Capacity
	if eligible {
		if _, err := s.repo.FindActiveBookingID(ctx, planID, userID); err == nil {
			eligible = false // already booked
		}
	}

	return &Quote{
		PriceMinor:      pricing.PriceMinor,
		ServiceFeeMinor: fee,
		TotalMinor:      pricing.PriceMinor + fee,
		Currency:        pricing.Currency,
		Eligible:        eligible,
	}, nil
}

// CancelBooking always succeeds in transitioning the booking to cancelled —
// whether a refund follows is a separate decision made downstream (see
// cmd/worker's BOOKING_CANCELLED handler and its RefundWindowHours constant),
// not something this method gates.
func (s *Service) CancelBooking(ctx context.Context, bookingID, reason string) (*Booking, error) {
	if bookingID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Cancel(ctx, bookingID, reason)
}

func (s *Service) CheckIn(ctx context.Context, bookingID, checkedInBy string) (*Booking, error) {
	if bookingID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.CheckIn(ctx, bookingID, checkedInBy)
}
