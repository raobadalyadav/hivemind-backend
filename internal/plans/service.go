package plans

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidInput = errors.New("plans: invalid input")
	ErrForbidden    = errors.New("plans: caller is not the host of this plan")
)

const defaultSearchLimit = 20

// BookingCreator/BookingCanceller are satisfied by *bookings.Service (wired
// in cmd/api/main.go) — declared here, not imported from internal/bookings,
// so neither package needs to import the other.
type BookingCreator interface {
	CreateBookingForPlan(ctx context.Context, planID, userID, idempotencyKey string) (bookingID string, err error)
}

type BookingCanceller interface {
	CancelBookingForPlan(ctx context.Context, planID, userID string) error
}

type Service struct {
	repo      *Repository
	creator   BookingCreator
	canceller BookingCanceller
	draftGen  DraftGenerator
}

func NewService(repo *Repository, creator BookingCreator, canceller BookingCanceller, draftGen DraftGenerator) *Service {
	return &Service{repo: repo, creator: creator, canceller: canceller, draftGen: draftGen}
}

// SuggestPlanDraft is purely advisory — the host still calls CreatePlan
// with whatever title/description they end up choosing; this writes
// nothing.
func (s *Service) SuggestPlanDraft(ctx context.Context, categoryID string) (title, description string, err error) {
	categoryName, err := s.repo.GetCategoryName(ctx, categoryID)
	if err != nil {
		return "", "", err
	}
	title, description = s.draftGen.Suggest(ctx, categoryName)
	return title, description, nil
}

func (s *Service) CreatePlan(ctx context.Context, p *Plan) (*Plan, error) {
	if p.Title == "" || p.HostID == "" || p.Capacity <= 0 {
		return nil, ErrInvalidInput
	}
	if p.Currency == "" {
		p.Currency = "INR"
	}
	if p.StartsAt.IsZero() {
		p.StartsAt = time.Now()
	}
	return s.repo.Create(ctx, p)
}

func (s *Service) GetPlan(ctx context.Context, id string) (*Plan, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

// GetPlanHostID satisfies promotions.PlanHostChecker — PurchasePromotion
// uses it to verify the caller actually hosts the plan being promoted,
// without importing this package's concrete Plan type.
func (s *Service) GetPlanHostID(ctx context.Context, id string) (string, error) {
	p, err := s.GetPlan(ctx, id)
	if err != nil {
		return "", err
	}
	return p.HostID, nil
}

func (s *Service) SearchPlans(ctx context.Context, f SearchFilter) ([]*Plan, error) {
	return s.repo.Search(ctx, f, defaultSearchLimit)
}

func (s *Service) JoinPlan(ctx context.Context, planID, userID, idempotencyKey string) (string, error) {
	if planID == "" || userID == "" {
		return "", ErrInvalidInput
	}
	return s.creator.CreateBookingForPlan(ctx, planID, userID, idempotencyKey)
}

func (s *Service) LeavePlan(ctx context.Context, planID, userID string) error {
	if planID == "" || userID == "" {
		return ErrInvalidInput
	}
	return s.canceller.CancelBookingForPlan(ctx, planID, userID)
}

func isAdminRole(role string) bool {
	return role == "admin" || role == "super_admin"
}

// CancelPlan requires the caller to be the plan's host or an admin — a
// plan-cancellation call from anyone else is rejected before any write
// happens (see pkg/grpcmiddleware's identical isAdminRole check; duplicated
// here rather than imported, since it's two lines and importing a
// middleware package into a service layer would be the wrong direction).
func (s *Service) CancelPlan(ctx context.Context, planID, callerID, callerRole, reason string) (*Plan, error) {
	if planID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	plan, err := s.repo.Get(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan.HostID != callerID && !isAdminRole(callerRole) {
		return nil, ErrForbidden
	}
	return s.repo.Cancel(ctx, planID, reason)
}
