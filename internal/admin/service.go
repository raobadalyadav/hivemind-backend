package admin

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidInput = errors.New("admin: invalid input")
	ErrNotFound     = errors.New("admin: not found")
)

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
	// Only terminal bookkeeping statuses: this writes bookings.status
	// directly and does not touch confirmed_count, plan_participants or the
	// waitlist, so overriding to 'cancelled'/'confirmed' would leak or
	// double-count a seat. Cancellation must go through bookings.Cancel.
	if bookingID == "" || (newStatus != "refunded" && newStatus != "no_show" && newStatus != "attended") {
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

func (s *Service) CreateCity(ctx context.Context, name, state, country, actorID string) (*City, error) {
	if name == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.CreateCity(ctx, name, state, country, actorID)
}

func (s *Service) UpdateCityStatus(ctx context.Context, cityID, status, actorID string) error {
	if cityID == "" || status == "" {
		return ErrInvalidInput
	}
	return s.repo.UpdateCityStatus(ctx, cityID, status, actorID)
}

func (s *Service) ListSOSEvents(ctx context.Context, onlyOpen bool, limit int32) ([]SOSEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}
	return s.repo.ListSOSEvents(ctx, onlyOpen, int(limit))
}

func (s *Service) AcknowledgeSOSEvent(ctx context.Context, sosID, actorID string) error {
	if sosID == "" || actorID == "" {
		return ErrInvalidInput
	}
	return s.repo.AcknowledgeSOSEvent(ctx, sosID, actorID)
}

func (s *Service) CreateExternalEvent(ctx context.Context, e ExternalEvent, actorID string) (*ExternalEvent, error) {
	e.Title = strings.TrimSpace(e.Title)
	if e.CityID == "" || e.Title == "" || len(e.Title) > 200 || e.StartsAt.IsZero() || actorID == "" ||
		(e.EndsAt != nil && !e.EndsAt.After(e.StartsAt)) || len(e.SourceURL) > 2048 || len(e.ImageURL) > 2048 ||
		!(strings.HasPrefix(e.SourceURL, "https://") || strings.HasPrefix(e.SourceURL, "http://")) ||
		(e.ImageURL != "" && !strings.HasPrefix(e.ImageURL, "https://")) {
		return nil, ErrInvalidInput
	}
	out, err := s.repo.CreateExternalEvent(ctx, e, actorID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "22P02") { // unknown city/category
		return nil, ErrInvalidInput
	}
	return out, err
}

func (s *Service) DeactivateExternalEvent(ctx context.Context, eventID, actorID string) error {
	if eventID == "" || actorID == "" {
		return ErrInvalidInput
	}
	err := s.repo.DeactivateExternalEvent(ctx, eventID, actorID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return ErrNotFound
	}
	return err
}
