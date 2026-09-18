package admin

import (
	"context"
	"errors"
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
