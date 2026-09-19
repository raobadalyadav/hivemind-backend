package company

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput  = errors.New("company: invalid input")
	ErrForbidden     = errors.New("company: caller is not this company's owner")
	ErrNotAllMembers = errors.New("company: one or more employee_user_ids are not members of this company")
)

// BookingCreator is satisfied by *bookings.Service — declared here so this
// package doesn't import internal/bookings concretely. CreateTeamBooking
// reuses this exact capacity-enforced path per employee rather than
// duplicating booking/locking logic.
type BookingCreator interface {
	CreateBookingForPlan(ctx context.Context, planID, userID, idempotencyKey string) (bookingID string, err error)
}

type Service struct {
	repo     *Repository
	bookings BookingCreator
}

func NewService(repo *Repository, bookings BookingCreator) *Service {
	return &Service{repo: repo, bookings: bookings}
}

func (s *Service) CreateCompany(ctx context.Context, name, billingEmail, ownerID string) (*Company, error) {
	if name == "" || ownerID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, name, billingEmail, ownerID)
}

// AddCompanyMember requires the caller to own the company — same
// fetch-then-compare ownership shape used throughout this codebase.
func (s *Service) AddCompanyMember(ctx context.Context, companyID, callerID, employeeUserID string) (*Member, error) {
	if companyID == "" || callerID == "" || employeeUserID == "" {
		return nil, ErrInvalidInput
	}
	ok, err := s.repo.IsOwner(ctx, companyID, callerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrForbidden
	}
	return s.repo.AddMember(ctx, companyID, employeeUserID)
}

// ListCompanyMembers is readable by any member, not just the owner.
func (s *Service) ListCompanyMembers(ctx context.Context, companyID, callerID string) ([]*Member, error) {
	if companyID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	isMember, err := s.repo.IsMember(ctx, companyID, callerID)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, ErrForbidden
	}
	return s.repo.ListMembers(ctx, companyID, 200)
}

// TeamBookingResult is deliberately not "all-or-nothing": a gRPC error
// response can't also carry a response body, so partial success (some
// employees booked before one hits, e.g., a full plan) is reported back as
// data — FailedEmployeeID/Err are empty/nil on full success.
type TeamBookingResult struct {
	BookingIDs       []string
	FailedEmployeeID string
	Err              error
}

// CreateTeamBooking is owner-only and loops the existing, already
// capacity-enforced bookings.Service.CreateBookingForPlan per employee —
// no duplicated capacity/locking logic. A deterministic per-employee
// idempotency key means a retried call never double-books the same
// employee on the same plan. Billing is attribution only (company_id tag)
// — no consolidated payment is created; each employee's booking is billed
// the same honest way payouts are recorded, per the established non-goal
// against fake invoicing/billing integrations.
func (s *Service) CreateTeamBooking(ctx context.Context, companyID, callerID, planID string, employeeUserIDs []string) (*TeamBookingResult, error) {
	if companyID == "" || callerID == "" || planID == "" || len(employeeUserIDs) == 0 {
		return nil, ErrInvalidInput
	}
	ok, err := s.repo.IsOwner(ctx, companyID, callerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrForbidden
	}

	// Reject up front rather than letting an owner attribute a booking to
	// an arbitrary, non-member user_id on the company's behalf — a HIGH
	// severity IDOR flagged by security review, fixed by verifying
	// membership before any booking is created, not just company ownership.
	allMembers, err := s.repo.AllAreMembers(ctx, companyID, employeeUserIDs)
	if err != nil {
		return nil, err
	}
	if !allMembers {
		return nil, ErrNotAllMembers
	}

	result := &TeamBookingResult{}
	for _, employeeID := range employeeUserIDs {
		key := "team:" + companyID + ":" + planID + ":" + employeeID
		bookingID, err := s.bookings.CreateBookingForPlan(ctx, planID, employeeID, key)
		if err != nil {
			result.FailedEmployeeID = employeeID
			result.Err = err
			return result, nil
		}
		if err := s.repo.TagBookingCompany(ctx, bookingID, companyID); err != nil {
			result.FailedEmployeeID = employeeID
			result.Err = err
			return result, nil
		}
		result.BookingIDs = append(result.BookingIDs, bookingID)
	}
	return result, nil
}
