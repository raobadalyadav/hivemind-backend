// Package safety implements flow.md §44/§45: the Safety Center, emergency
// contact and SOS. SOS is deliberately modest and honest — it records the
// alert and emails the user's contact if configured; it never claims to
// dispatch emergency services.
package safety

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidInput   = errors.New("safety: invalid input")
	ErrNotConfirmed   = errors.New("safety: SOS must be explicitly confirmed")
	ErrPlanNotAllowed = errors.New("safety: you can only attach a plan you host or attend")
	ErrNoContact      = errors.New("safety: no emergency contact set")
)

type Contact struct {
	Name, Email, Phone, Relationship string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) SetContact(ctx context.Context, userID string, c Contact) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO emergency_contacts (user_id, name, email, phone, relationship) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (user_id) DO UPDATE SET name = EXCLUDED.name, email = EXCLUDED.email,
			phone = EXCLUDED.phone, relationship = EXCLUDED.relationship, updated_at = now()`,
		userID, c.Name, c.Email, c.Phone, c.Relationship)
	return err
}

func (r *Repository) GetContact(ctx context.Context, userID string) (*Contact, error) {
	var c Contact
	err := r.pool.QueryRow(ctx, `SELECT name, email, phone, relationship FROM emergency_contacts WHERE user_id = $1`, userID).
		Scan(&c.Name, &c.Email, &c.Phone, &c.Relationship)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoContact
	}
	return &c, err
}

func (r *Repository) DeleteContact(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM emergency_contacts WHERE user_id = $1`, userID)
	return err
}

type Center struct {
	VerificationStatus string
	HasContact         bool
	BlockedCount       int32
}

func (r *Repository) Center(ctx context.Context, userID string) (*Center, error) {
	var c Center
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT verification_status::text FROM user_profiles WHERE user_id = $1), 'unverified'),
			EXISTS(SELECT 1 FROM emergency_contacts WHERE user_id = $1),
			(SELECT count(*) FROM blocks WHERE user_id = $1)`, userID).Scan(&c.VerificationStatus, &c.HasContact, &c.BlockedCount)
	return &c, err
}

// PlanContext returns the plan's details for an SOS message, but only when
// the caller hosts it or is a confirmed participant (verified server-side —
// the client can't attach an arbitrary plan to an alert).
type PlanInfo struct {
	Title, Venue string
	StartsAt     time.Time
}

func (r *Repository) PlanContext(ctx context.Context, planID, userID string) (*PlanInfo, error) {
	var p PlanInfo
	err := r.pool.QueryRow(ctx, `
		SELECT p.title, COALESCE(v.name, ''), p.starts_at
		FROM plans p LEFT JOIN venues v ON v.id = p.venue_id
		WHERE p.id = $1 AND (p.host_id = $2
			OR EXISTS (SELECT 1 FROM plan_participants pp WHERE pp.plan_id = p.id AND pp.user_id = $2 AND pp.status = 'confirmed'))`,
		planID, userID).Scan(&p.Title, &p.Venue, &p.StartsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPlanNotAllowed
	}
	if err != nil && isBadUUID(err) {
		return nil, ErrInvalidInput
	}
	return &p, err
}

func (r *Repository) DisplayName(ctx context.Context, userID string) string {
	var n string
	_ = r.pool.QueryRow(ctx, `SELECT display_name FROM user_profiles WHERE user_id = $1`, userID).Scan(&n)
	if n == "" {
		return "A HiveMind user"
	}
	return n
}

type SOS struct {
	UserID, PlanID string
	Lat, Lng       *float64
	Note           string
}

// RecordSOS returns an existing event if this user already raised one in the
// last `dedupe` (so a nervous double-tap can't spam the contact); fresh is
// true only for a newly inserted event. The advisory lock makes the
// check-then-insert race-free.
func (r *Repository) RecordSOS(ctx context.Context, s SOS, dedupe time.Duration, now time.Time) (id string, fresh bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "sos:"+s.UserID); err != nil {
		return "", false, err
	}
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM sos_events WHERE user_id = $1 AND created_at > $2::timestamptz - $3::float8 * interval '1 second'
		ORDER BY created_at DESC LIMIT 1`, s.UserID, now, dedupe.Seconds()).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO sos_events (user_id, plan_id, latitude, longitude, note, contact_delivery, created_at)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5, 'no_contact', $6::timestamptz) RETURNING id::text`,
		s.UserID, s.PlanID, s.Lat, s.Lng, s.Note, now).Scan(&id); err != nil {
		return "", false, err
	}
	return id, true, tx.Commit(ctx)
}

func (r *Repository) SetDelivery(ctx context.Context, sosID, delivery, deliveryErr string) error {
	_, err := r.pool.Exec(ctx, `UPDATE sos_events SET contact_delivery = $2, delivery_error = $3 WHERE id = $1`, sosID, delivery, deliveryErr)
	return err
}

func isBadUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
