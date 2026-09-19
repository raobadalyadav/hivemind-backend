// Package venues implements PRD §13.12 Venue Marketplace — every RPC is
// fully implemented.
package venues

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrVenueNotFound = errors.New("venues: venue not found")

type Venue struct {
	ID          string
	OwnerHostID string
	CityID      string
	Name        string
	Address     string
	Latitude    *float64
	Longitude   *float64
	Capacity    int32
}

type Dashboard struct {
	TotalBookings     int64
	TotalAttendees    int64
	GrossRevenueMinor int64
	AvgRating         float64
	RepeatVisitors    []*RepeatVisitor
}

type RepeatVisitor struct {
	UserID     string
	VisitCount int64
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, v *Venue) (*Venue, error) {
	out := *v
	err := r.pool.QueryRow(ctx, `
		INSERT INTO venues (owner_host_id, city_id, name, address, capacity, location)
		VALUES ($1, $2, $3, $4, $5,
			CASE WHEN $6::float8 IS NULL THEN NULL
			     ELSE ST_SetSRID(ST_MakePoint($7, $6), 4326)::geography END)
		RETURNING id`,
		v.OwnerHostID, v.CityID, v.Name, v.Address, v.Capacity, v.Latitude, v.Longitude,
	).Scan(&out.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Venue, error) {
	var v Venue
	err := r.pool.QueryRow(ctx, `
		SELECT id, COALESCE(owner_host_id::text,''), city_id::text, name, address, capacity,
			ST_Y(location::geometry), ST_X(location::geometry)
		FROM venues WHERE id = $1`, id,
	).Scan(&v.ID, &v.OwnerHostID, &v.CityID, &v.Name, &v.Address, &v.Capacity, &v.Latitude, &v.Longitude)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVenueNotFound
		}
		return nil, err
	}
	return &v, nil
}

func (r *Repository) ListByOwner(ctx context.Context, ownerHostID string, limit int) ([]*Venue, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, COALESCE(owner_host_id::text,''), city_id::text, name, address, capacity,
			ST_Y(location::geometry), ST_X(location::geometry)
		FROM venues WHERE owner_host_id = $1 ORDER BY name LIMIT $2`, ownerHostID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Venue
	for rows.Next() {
		var v Venue
		if err := rows.Scan(&v.ID, &v.OwnerHostID, &v.CityID, &v.Name, &v.Address, &v.Capacity, &v.Latitude, &v.Longitude); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

func (r *Repository) Update(ctx context.Context, id, name, address string, capacity int32) (*Venue, error) {
	var v Venue
	err := r.pool.QueryRow(ctx, `
		UPDATE venues SET name = $2, address = $3, capacity = $4 WHERE id = $1
		RETURNING id, COALESCE(owner_host_id::text,''), city_id::text, name, address, capacity,
			ST_Y(location::geometry), ST_X(location::geometry)`,
		id, name, address, capacity,
	).Scan(&v.ID, &v.OwnerHostID, &v.CityID, &v.Name, &v.Address, &v.Capacity, &v.Latitude, &v.Longitude)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVenueNotFound
		}
		return nil, err
	}
	return &v, nil
}

// GetDashboard aggregates bookings/attendees/revenue/rating for every plan
// hosted at this venue. Same query shape as internal/host's host dashboard
// (WHERE pl.venue_id = $1 instead of pl.host_id = $1) — not worth sharing
// for two call sites.
func (r *Repository) GetDashboard(ctx context.Context, venueID string) (*Dashboard, error) {
	var d Dashboard
	err := r.pool.QueryRow(ctx, `
		SELECT
			COUNT(DISTINCT b.id) FILTER (WHERE b.status IN ('confirmed'::booking_status,'attended'::booking_status)),
			COUNT(DISTINCT b.user_id) FILTER (WHERE b.status = 'attended'::booking_status),
			COALESCE(SUM(p.amount_minor) FILTER (WHERE p.status = 'captured'::payment_status), 0),
			COALESCE((SELECT AVG(rv.rating) FROM reviews rv JOIN plans pl2 ON pl2.id = rv.plan_id WHERE pl2.venue_id = $1), 0)
		FROM plans pl
		LEFT JOIN bookings b ON b.plan_id = pl.id
		LEFT JOIN orders o ON o.booking_id = b.id
		LEFT JOIN payments p ON p.order_id = o.id
		WHERE pl.venue_id = $1`,
		venueID,
	).Scan(&d.TotalBookings, &d.TotalAttendees, &d.GrossRevenueMinor, &d.AvgRating)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// RepeatVisitors returns attendees with more than one attended booking at
// plans hosted at this venue — the "CRM-like repeat visitor" feature named
// in prd_docs.md §12, gated on the venue_pro entitlement by the service layer.
func (r *Repository) RepeatVisitors(ctx context.Context, venueID string, limit int) ([]*RepeatVisitor, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT b.user_id::text, COUNT(*) AS visits
		FROM bookings b
		JOIN plans pl ON pl.id = b.plan_id
		WHERE pl.venue_id = $1 AND b.status = 'attended'::booking_status
		GROUP BY b.user_id
		HAVING COUNT(*) > 1
		ORDER BY visits DESC
		LIMIT $2`,
		venueID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*RepeatVisitor
	for rows.Next() {
		var v RepeatVisitor
		if err := rows.Scan(&v.UserID, &v.VisitCount); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}
