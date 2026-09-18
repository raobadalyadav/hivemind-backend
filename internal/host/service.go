package host

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput     = errors.New("host: invalid input")
	ErrHostNotApproved  = errors.New("host: payout account is not active")
	ErrNoPayoutBalance  = errors.New("host: no payable balance")
)

// Commission per PRD §21: 15% flat, 10% for hosts holding the business_pro
// entitlement. A Go const, not a DB-configurable rate table — nothing has
// asked for per-host rates yet.
const (
	commissionRate         = 0.15
	businessCommissionRate = 0.10
	businessEntitlementKey = "business_pro"
)

// EntitlementChecker is satisfied by *subscriptions.Service (wired in
// cmd/api/main.go) — RequestPayout uses it to look up the host's
// commission tier without importing internal/subscriptions concretely.
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

func (s *Service) ApplyForHost(ctx context.Context, hostID, idDocumentURL, bankAccountNumber, bankIFSC string) (*PayoutAccount, error) {
	if hostID == "" || idDocumentURL == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ApplyForHost(ctx, hostID, idDocumentURL, bankAccountNumber, bankIFSC)
}

func (s *Service) GetHostStatus(ctx context.Context, hostID string) (*PayoutAccount, error) {
	if hostID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.GetPayoutAccountByHostID(ctx, hostID)
}

// RequestPayout computes the payable balance server-side — never trusts a
// client-supplied amount. No real bank-transfer call is made here: the
// payout row is recorded, and admin.MarkPayoutProcessed is what an
// operator calls once money has actually moved — same non-goal already
// established for subscriptions.Subscribe (trusted receipt, no real
// Apple/Google verification call).
func (s *Service) RequestPayout(ctx context.Context, hostID string) (*Payout, error) {
	if hostID == "" {
		return nil, ErrInvalidInput
	}
	account, err := s.repo.GetPayoutAccountByHostID(ctx, hostID)
	if err != nil {
		return nil, err
	}
	if account.Status != "active" {
		return nil, ErrHostNotApproved
	}

	netCaptured, priorPayouts, err := s.repo.GetPayoutBalance(ctx, hostID)
	if err != nil {
		return nil, err
	}

	rate := commissionRate
	if s.entitlements != nil {
		if ok, _ := s.entitlements.HasEntitlement(ctx, hostID, businessEntitlementKey); ok {
			rate = businessCommissionRate
		}
	}
	available := netCaptured - int64(float64(netCaptured)*rate) - priorPayouts
	if available <= 0 {
		return nil, ErrNoPayoutBalance
	}
	return s.repo.CreatePayout(ctx, account.ID, available)
}

func (s *Service) ListPayouts(ctx context.Context, hostID string) ([]*Payout, error) {
	if hostID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListPayouts(ctx, hostID, 50)
}

func (s *Service) GetHostDashboard(ctx context.Context, hostID string) (*Dashboard, error) {
	if hostID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.GetDashboard(ctx, hostID)
}
