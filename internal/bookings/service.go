package bookings

import (
	"context"
	"errors"
	"time"

	"github.com/hivemind/backend/pkg/idempotency"
)

var (
	ErrInvalidInput = errors.New("bookings: invalid input")
	ErrForbidden    = errors.New("bookings: caller is not authorized for this booking")
)

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
	repo         *Repository
	guard        *idempotency.Guard
	passSecret   []byte
	entitlements EntitlementChecker
}

func NewService(repo *Repository, guard *idempotency.Guard) *Service {
	return &Service{repo: repo, guard: guard}
}

func (s *Service) CreateBooking(ctx context.Context, b *Booking) (*Booking, error) {
	if b.PlanID == "" || b.UserID == "" {
		return nil, ErrInvalidInput
	}
	// Access first: a denied request must not burn its idempotency key.
	if err := s.checkAccess(ctx, b.PlanID, b.UserID); err != nil {
		return nil, err
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

func isAdminRole(role string) bool {
	return role == "admin" || role == "super_admin"
}

// authorizeForBooking checks the caller is the booking's own user, the
// plan's host, or an admin — the shared gate GetBooking/CancelBookingAsUser/
// CheckIn all apply before touching a booking that isn't a fresh Create.
func (s *Service) authorizeForBooking(ctx context.Context, b *Booking, callerID, callerRole string) error {
	if b.UserID == callerID || isAdminRole(callerRole) {
		return nil
	}
	hostID, err := s.repo.GetPlanHostID(ctx, b.PlanID)
	if err != nil {
		return err
	}
	if hostID == callerID {
		return nil
	}
	return ErrForbidden
}

// GetBooking is for internal/system callers that have already established
// authorization elsewhere (there are currently none, but this stays
// unauthenticated-by-default so a future internal caller doesn't need to
// fabricate a caller identity). The gRPC handler uses GetBookingAsUser.
func (s *Service) GetBooking(ctx context.Context, id string) (*Booking, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

// GetBookingForReview satisfies reviews.BookingChecker — CreateReview uses
// it to derive plan_id/user_id from the booking itself (never the request)
// and to require the booking be 'attended' before a review can exist.
func (s *Service) GetBookingForReview(ctx context.Context, id string) (planID, userID, status string, err error) {
	b, err := s.GetBooking(ctx, id)
	if err != nil {
		return "", "", "", err
	}
	return b.PlanID, b.UserID, b.Status, nil
}

// GetBookingOwnerID satisfies payments.BookingOwnerChecker — payments.CreateOrder
// uses it to verify the caller actually owns the booking they're paying
// for, without importing this package's concrete Booking type.
func (s *Service) GetBookingOwnerID(ctx context.Context, id string) (string, error) {
	b, err := s.GetBooking(ctx, id)
	if err != nil {
		return "", err
	}
	return b.UserID, nil
}

// GetBookingAsUser is what the gRPC handler calls — only the booking's own
// user, the plan's host, or an admin may read it (booking price/status is
// not public data).
func (s *Service) GetBookingAsUser(ctx context.Context, id, callerID, callerRole string) (*Booking, error) {
	b, err := s.GetBooking(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeForBooking(ctx, b, callerID, callerRole); err != nil {
		return nil, err
	}
	return b, nil
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

	holds, err := s.repo.HoldsForOthers(ctx, planID, userID)
	if err != nil {
		return nil, err
	}
	eligible := pricing.Status == "published" && pricing.ConfirmedCount+holds < pricing.Capacity
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

// CancelBooking is the unauthenticated core used by internal callers that
// already established authorization upstream: CancelBookingForPlan (the
// caller left their own plan — plans.Service already sourced that userID
// from the JWT) and CancelAllForPlan (the system fanning out a cancellation
// plans.Service already authorized as a host/admin action). It always
// succeeds in transitioning the booking to cancelled — whether a refund
// follows is a separate decision made downstream (cmd/worker's
// BOOKING_CANCELLED handler), not something this method gates.
func (s *Service) CancelBooking(ctx context.Context, bookingID, reason string) (*Booking, error) {
	if bookingID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Cancel(ctx, bookingID, reason)
}

// CancelBookingAsUser is what the gRPC handler calls — only the booking's
// own user, the plan's host, or an admin may cancel it.
func (s *Service) CancelBookingAsUser(ctx context.Context, bookingID, callerID, callerRole, reason string) (*Booking, error) {
	if bookingID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	b, err := s.GetBooking(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeForBooking(ctx, b, callerID, callerRole); err != nil {
		return nil, err
	}
	return s.CancelBooking(ctx, bookingID, reason)
}

// CheckIn requires the caller to be the plan's host or an admin — the
// person scanning a QR code at the door, not the attendee themselves (see
// flow.md §34: "Host scans → Check-in successful").
func (s *Service) CheckIn(ctx context.Context, bookingID, callerID, callerRole string) (*Booking, error) {
	if bookingID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	b, err := s.GetBooking(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if !isAdminRole(callerRole) {
		hostID, err := s.repo.GetPlanHostID(ctx, b.PlanID)
		if err != nil {
			return nil, err
		}
		if hostID != callerID {
			return nil, ErrForbidden
		}
	}
	return s.repo.CheckIn(ctx, bookingID, callerID)
}

func (s *Service) JoinWaitlist(ctx context.Context, planID, userID string) (*WaitlistStatus, error) {
	if planID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	if err := s.checkAccess(ctx, planID, userID); err != nil {
		return nil, err
	}
	return s.repo.JoinWaitlist(ctx, planID, userID)
}

func (s *Service) LeaveWaitlist(ctx context.Context, planID, userID string) error {
	if planID == "" || userID == "" {
		return ErrInvalidInput
	}
	return s.repo.LeaveWaitlist(ctx, planID, userID)
}

func (s *Service) GetWaitlistStatus(ctx context.Context, planID, userID string) (*WaitlistStatus, error) {
	if planID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.GetWaitlistStatus(ctx, planID, userID)
}

func (s *Service) ListMyWaitlist(ctx context.Context, userID string) ([]*WaitlistStatus, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListMyWaitlist(ctx, userID)
}

// SweepWaitlist / ExpireWaitlistForPlan are called by cmd/worker.
func (s *Service) SweepWaitlist(ctx context.Context, now time.Time) (int, error) {
	return s.repo.SweepWaitlist(ctx, now)
}

func (s *Service) ExpireWaitlistForPlan(ctx context.Context, planID string) error {
	return s.repo.ExpireWaitlistForPlan(ctx, planID)
}

func (s *Service) CompleteEndedPlans(ctx context.Context, now time.Time) (int, error) {
	return s.repo.CompleteEndedPlans(ctx, now)
}

func (s *Service) MarkNoShows(ctx context.Context, now time.Time) (int, error) {
	return s.repo.MarkNoShows(ctx, now)
}

func (s *Service) ListMyBookings(ctx context.Context, userID, tab string) ([]*BookingSummary, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListMyBookings(ctx, userID, tab)
}
