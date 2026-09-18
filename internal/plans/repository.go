// Package plans implements PRD §13.4 Plans. CreatePlan/GetPlan are the fully
// working vertical slice for this scaffold; the rest of PlanService is a
// typed stub (see grpc.go).
package plans

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Plan struct {
	ID             string
	Title          string
	Description    string
	CategoryID     string
	HostID         string
	CityID         string
	VenueID        string
	StartsAt       time.Time
	EndsAt         time.Time
	Capacity       int32
	ConfirmedCount int32
	PriceMinor     int64
	Currency       string
	Status         string
	Latitude       *float64
	Longitude      *float64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, p *Plan) (*Plan, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO plans (title, description, category_id, host_id, city_id, venue_id,
			starts_at, ends_at, capacity, price_minor, currency, status, location)
		VALUES ($1, $2, NULLIF($3,'')::uuid, $4::uuid, NULLIF($5,'')::uuid, NULLIF($6,'')::uuid,
			$7, $8, $9, $10, $11, 'draft',
			CASE WHEN $12::float8 IS NULL THEN NULL
			     ELSE ST_SetSRID(ST_MakePoint($13, $12), 4326)::geography END)
		RETURNING id, confirmed_count, status, created_at, updated_at`,
		p.Title, p.Description, p.CategoryID, p.HostID, p.CityID, p.VenueID,
		p.StartsAt, p.EndsAt, p.Capacity, p.PriceMinor, p.Currency,
		p.Latitude, p.Longitude,
	)

	out := *p
	if err := row.Scan(&out.ID, &out.ConfirmedCount, &out.Status, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Plan, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, title, description, COALESCE(category_id::text,''), host_id::text,
			COALESCE(city_id::text,''), COALESCE(venue_id::text,''), starts_at, ends_at,
			capacity, confirmed_count, price_minor, currency, status,
			ST_Y(location::geometry), ST_X(location::geometry), created_at, updated_at
		FROM plans WHERE id = $1`, id)

	var p Plan
	if err := row.Scan(&p.ID, &p.Title, &p.Description, &p.CategoryID, &p.HostID,
		&p.CityID, &p.VenueID, &p.StartsAt, &p.EndsAt, &p.Capacity, &p.ConfirmedCount,
		&p.PriceMinor, &p.Currency, &p.Status, &p.Latitude, &p.Longitude,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}
