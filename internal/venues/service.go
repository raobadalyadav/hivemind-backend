package venues

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput = errors.New("venues: invalid input")
	ErrForbidden    = errors.New("venues: caller does not own this venue")
)

const defaultVenuePageSize = 50

// venueEntitlementKey gates the repeat-visitor CRM feature — reuses
// internal/subscriptions' existing generic entitlement mechanism exactly
// as internal/host does for business_pro, not a second mechanism.
const venueEntitlementKey = "venue_pro"

// EntitlementChecker is satisfied by *subscriptions.Service (wired in
// cmd/api/main.go) — same interface shape as host.EntitlementChecker.
type EntitlementChecker interface {
	HasEntitlement(ctx context.Context, userID, key string) (bool, error)
}

type Service struct {
	repo         *Repository
	entitlements EntitlementChecker
}

func NewService(repo *Repository, entitlements EntitlementChecker) *Service {
	return &Service{repo: repo, entitlements: entitlements}
}

func (s *Service) CreateVenue(ctx context.Context, v *Venue) (*Venue, error) {
	if v.OwnerHostID == "" || v.Name == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, v)
}

func (s *Service) GetVenue(ctx context.Context, id string) (*Venue, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) ListMyVenues(ctx context.Context, ownerHostID string) ([]*Venue, error) {
	if ownerHostID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListByOwner(ctx, ownerHostID, defaultVenuePageSize)
}

// UpdateVenue and GetVenueDashboard both require the caller to own the
// venue — same fetch-then-compare ownership check used throughout this
// codebase (bookings, connections, subscriptions).
func (s *Service) UpdateVenue(ctx context.Context, id, callerID, name, address string, capacity int32) (*Venue, error) {
	if id == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	v, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.OwnerHostID != callerID {
		return nil, ErrForbidden
	}
	return s.repo.Update(ctx, id, name, address, capacity)
}

func (s *Service) GetVenueDashboard(ctx context.Context, venueID, callerID string) (*Dashboard, error) {
	if venueID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	v, err := s.repo.Get(ctx, venueID)
	if err != nil {
		return nil, err
	}
	if v.OwnerHostID != callerID {
		return nil, ErrForbidden
	}
	d, err := s.repo.GetDashboard(ctx, venueID)
	if err != nil {
		return nil, err
	}
	if s.entitlements != nil {
		if ok, _ := s.entitlements.HasEntitlement(ctx, callerID, venueEntitlementKey); ok {
			d.RepeatVisitors, _ = s.repo.RepeatVisitors(ctx, venueID, 50)
		}
	}
	return d, nil
}
