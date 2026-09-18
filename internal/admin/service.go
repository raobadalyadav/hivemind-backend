package admin

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidInput = errors.New("admin: invalid input")

const defaultPageSize = 50

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) ListUsers(ctx context.Context, query string) ([]string, error) {
	return s.repo.ListUsers(ctx, query, defaultPageSize)
}

func (s *Service) SuspendUser(ctx context.Context, userID, reason, actorID string) error {
	if userID == "" || reason == "" {
		return ErrInvalidInput
	}
	return s.repo.SuspendUser(ctx, userID, reason, actorID)
}

func (s *Service) ListReports(ctx context.Context, statusFilter string) ([]string, error) {
	return s.repo.ListReportCaseIDs(ctx, statusFilter, defaultPageSize)
}

func (s *Service) OverrideBookingStatus(ctx context.Context, bookingID, newStatus, reason, actorID string) error {
	if bookingID == "" || newStatus == "" {
		return ErrInvalidInput
	}
	return s.repo.OverrideBookingStatus(ctx, bookingID, newStatus, reason, actorID)
}

func (s *Service) GetDashboardStats(ctx context.Context, cityID string) (*DashboardStats, error) {
	return s.repo.GetDashboardStats(ctx, cityID)
}

func (s *Service) ApproveHost(ctx context.Context, userID, actorID string) error {
	if userID == "" {
		return ErrInvalidInput
	}
	return s.repo.ApproveHost(ctx, userID, actorID)
}

func (s *Service) MarkPayoutProcessed(ctx context.Context, payoutID, actorID string) error {
	if payoutID == "" {
		return ErrInvalidInput
	}
	return s.repo.MarkPayoutProcessed(ctx, payoutID, actorID)
}

func (s *Service) AdminGrantCredit(ctx context.Context, userID string, amountMinor int64, reason, actorID string) error {
	if userID == "" || amountMinor <= 0 {
		return ErrInvalidInput
	}
	return s.repo.GrantCredit(ctx, userID, amountMinor, reason, actorID)
}

func (s *Service) CreateCoupon(ctx context.Context, code, discountType string, discountValue int64, maxUses int32, expiresAt *time.Time) (*Coupon, error) {
	if code == "" || discountValue <= 0 {
		return nil, ErrInvalidInput
	}
	return s.repo.CreateCoupon(ctx, code, discountType, discountValue, maxUses, expiresAt)
}

func (s *Service) ListCoupons(ctx context.Context) ([]*Coupon, error) {
	return s.repo.ListCoupons(ctx, defaultPageSize)
}

func (s *Service) DeactivateCoupon(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidInput
	}
	return s.repo.DeactivateCoupon(ctx, id)
}
