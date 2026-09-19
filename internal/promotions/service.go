package promotions

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var (
	ErrInvalidInput = errors.New("promotions: invalid input")
	ErrForbidden    = errors.New("promotions: caller does not host this plan")
)

// promotionPricePerDayMinor is a Go const, not client-supplied and not a DB
// pricing table — nothing has asked for variable pricing yet.
const promotionPricePerDayMinor = 50000 // ₹500.00/day

// PlanHostChecker is satisfied by *plans.Service (wired in cmd/api/main.go).
type PlanHostChecker interface {
	GetPlanHostID(ctx context.Context, planID string) (string, error)
}

// GatewayClient mirrors payments.GatewayClient — satisfied directly by
// *cashfree.Client, reused here with zero changes to internal/payments.
type GatewayClient interface {
	CreateOrder(ctx context.Context, orderID string, amountMinor int64, currency, customerID, customerPhone, customerEmail string) (cfOrderID, paymentSessionID string, err error)
}

type Service struct {
	repo     *Repository
	planHost PlanHostChecker
	gateway  GatewayClient
	logger   *slog.Logger
}

func NewService(repo *Repository, planHost PlanHostChecker, gateway GatewayClient, logger *slog.Logger) *Service {
	return &Service{repo: repo, planHost: planHost, gateway: gateway, logger: logger}
}

// PurchasePromotion requires the caller to host the plan being promoted,
// then always records the listing — a failed/unconfigured gateway call
// degrades to "listing exists, no payment session yet", same pattern as
// payments.Service.CreateOrder.
func (s *Service) PurchasePromotion(ctx context.Context, planID, callerID, customerPhone string, durationDays int32) (*PromotedListing, string, error) {
	if planID == "" || callerID == "" || durationDays <= 0 {
		return nil, "", ErrInvalidInput
	}
	hostID, err := s.planHost.GetPlanHostID(ctx, planID)
	if err != nil {
		return nil, "", err
	}
	if hostID != callerID {
		return nil, "", ErrForbidden
	}

	now := time.Now()
	listing := &PromotedListing{
		PlanID:      planID,
		HostID:      callerID,
		AmountMinor: int64(durationDays) * promotionPricePerDayMinor,
		Currency:    "INR",
		StartsAt:    now,
		EndsAt:      now.AddDate(0, 0, int(durationDays)),
	}
	created, err := s.repo.Create(ctx, listing)
	if err != nil {
		return nil, "", err
	}

	if s.gateway == nil {
		s.logger.Warn("payment gateway not configured, promotion created without a payment session", "listing_id", created.ID)
		return created, "", nil
	}

	cfOrderID, sessionID, err := s.gateway.CreateOrder(ctx, created.ID, created.AmountMinor, created.Currency, callerID, customerPhone, "")
	if err != nil {
		s.logger.Error("cashfree create order failed", "error", err, "listing_id", created.ID)
		return created, "", nil
	}
	if err := s.repo.SetGatewayOrderID(ctx, created.ID, cfOrderID); err != nil {
		s.logger.Error("store gateway order id", "error", err, "listing_id", created.ID)
	}
	created.GatewayOrderID = cfOrderID
	return created, sessionID, nil
}

// MarkCaptured is called by cmd/api's Cashfree webhook HTTP handler as a
// fallback when the order id isn't a payments order — not part of
// GatewayClient since it's driven by an inbound webhook, not an outbound call.
func (s *Service) MarkCaptured(ctx context.Context, cashfreeOrderID, gatewayPaymentID string, amountMinor int64) error {
	listing, err := s.repo.FindByGatewayOrderID(ctx, cashfreeOrderID)
	if err != nil {
		return err
	}
	return s.repo.MarkPaid(ctx, listing.ID, gatewayPaymentID)
}

func (s *Service) ListMyPromotedListings(ctx context.Context, hostID string) ([]*PromotedListing, error) {
	if hostID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListByHost(ctx, hostID, 50)
}
