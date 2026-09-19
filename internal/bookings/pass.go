package bookings

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hivemind/backend/pkg/security"
)

var (
	ErrPassUnavailable = errors.New("bookings: pass is only available for confirmed or attended bookings")
	ErrPassDisabled    = errors.New("bookings: digital pass signing is not configured")
	ErrOutsideWindow   = errors.New("bookings: pass scanned outside the plan's check-in window")
	ErrInvalidPass     = errors.New("bookings: invalid or expired pass")
)

const (
	checkInOpensBefore = 2 * time.Hour
	checkInClosesAfter = 1 * time.Hour
)

type PassInfo struct {
	BookingID    string
	PlanID       string
	UserID       string
	Status       string
	PlanTitle    string
	PlanDesc     string
	StartsAt     time.Time
	EndsAt       time.Time
	HostID       string
	VenueName    string
	VenueAddress string
	HostName     string
	AttendeeName string
}

// planEnd treats a plan with no valid end (ends_at <= starts_at) as ending
// six hours after it starts, so its pass doesn't expire before it begins.
func planEnd(starts, ends time.Time) time.Time {
	if ends.After(starts) {
		return ends
	}
	return starts.Add(6 * time.Hour)
}

func (r *Repository) GetPassInfo(ctx context.Context, bookingID string) (*PassInfo, error) {
	var i PassInfo
	err := r.pool.QueryRow(ctx, `
		SELECT b.id::text, b.plan_id::text, b.user_id::text, b.status::text,
			p.title, p.description, p.starts_at, p.ends_at, p.host_id::text,
			COALESCE(v.name,''), COALESCE(v.address,''),
			COALESCE(hp.display_name,''), COALESCE(ap.display_name,'')
		FROM bookings b
		JOIN plans p ON p.id = b.plan_id
		LEFT JOIN venues v ON v.id = p.venue_id
		LEFT JOIN user_profiles hp ON hp.user_id = p.host_id
		LEFT JOIN user_profiles ap ON ap.user_id = b.user_id
		WHERE b.id = $1`, bookingID,
	).Scan(&i.BookingID, &i.PlanID, &i.UserID, &i.Status, &i.PlanTitle, &i.PlanDesc, &i.StartsAt, &i.EndsAt,
		&i.HostID, &i.VenueName, &i.VenueAddress, &i.HostName, &i.AttendeeName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBookingNotFound
	}
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// WithPassSecret enables GetPass/ScanPass. A setter rather than a
// NewService parameter so existing constructor call sites are untouched.
func (s *Service) WithPassSecret(secret []byte) *Service {
	s.passSecret = secret
	return s
}

// GetPass is owner-only — deliberately stricter than authorizeForBooking,
// which also admits the host and admins; a host must not be able to mint an
// attendee's pass.
func (s *Service) GetPass(ctx context.Context, bookingID, callerID string) (*PassInfo, string, time.Time, error) {
	if len(s.passSecret) == 0 {
		return nil, "", time.Time{}, ErrPassDisabled
	}
	if bookingID == "" || callerID == "" {
		return nil, "", time.Time{}, ErrInvalidInput
	}
	info, err := s.repo.GetPassInfo(ctx, bookingID)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if info.UserID != callerID {
		return nil, "", time.Time{}, ErrForbidden
	}
	if info.Status != "confirmed" && info.Status != "attended" {
		return nil, "", time.Time{}, ErrPassUnavailable
	}
	validUntil := planEnd(info.StartsAt, info.EndsAt).Add(checkInClosesAfter)
	return info, security.SignPass(s.passSecret, info.BookingID, validUntil), validUntil, nil
}

// ScanPass is the host-side QR scan: verify the signature, require the
// caller to be the plan's host (or an admin), enforce the check-in window,
// then reuse the existing CheckIn. A replayed scan fails because CheckIn
// only transitions from 'confirmed'.
func (s *Service) ScanPass(ctx context.Context, payload, callerID, callerRole string) (*Booking, string, error) {
	if len(s.passSecret) == 0 {
		return nil, "", ErrPassDisabled
	}
	if payload == "" || callerID == "" {
		return nil, "", ErrInvalidInput
	}
	bookingID, err := security.VerifyPass(s.passSecret, payload, time.Now())
	if err != nil {
		return nil, "", ErrInvalidPass
	}
	info, err := s.repo.GetPassInfo(ctx, bookingID)
	if err != nil {
		return nil, "", err
	}
	if info.HostID != callerID && !isAdminRole(callerRole) {
		return nil, "", ErrForbidden
	}
	now := time.Now()
	if now.Before(info.StartsAt.Add(-checkInOpensBefore)) || now.After(planEnd(info.StartsAt, info.EndsAt).Add(checkInClosesAfter)) {
		return nil, "", ErrOutsideWindow
	}
	b, err := s.repo.CheckIn(ctx, bookingID, callerID)
	if err != nil {
		return nil, "", err
	}
	return b, info.AttendeeName, nil
}
