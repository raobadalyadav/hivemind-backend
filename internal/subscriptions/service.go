package subscriptions

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput = errors.New("subscriptions: invalid input")
	ErrForbidden    = errors.New("subscriptions: caller does not own this subscription")
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetCatalog(ctx context.Context) ([]*Product, error) {
	return s.repo.ListProducts(ctx)
}

func (s *Service) Subscribe(ctx context.Context, userID, productID, storeReceipt string) (*Subscription, error) {
	if userID == "" || productID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Subscribe(ctx, userID, productID, storeReceipt)
}

// CancelSubscription requires the caller to own the subscription — same
// ownership-check shape used throughout this codebase (bookings, connections).
func (s *Service) CancelSubscription(ctx context.Context, subscriptionID, callerID string) (*Subscription, error) {
	if subscriptionID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	sub, err := s.repo.Get(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub.UserID != callerID {
		return nil, ErrForbidden
	}
	return s.repo.Cancel(ctx, subscriptionID)
}

func (s *Service) GetEntitlements(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListEntitlements(ctx, userID)
}

// HasEntitlement satisfies host.EntitlementChecker (wired in cmd/api/main.go).
func (s *Service) HasEntitlement(ctx context.Context, userID, key string) (bool, error) {
	if userID == "" || key == "" {
		return false, nil
	}
	return s.repo.HasEntitlement(ctx, userID, key)
}
