// Package plans implements PRD §13.4 Plans — every RPC is fully implemented.
// JoinPlan/LeavePlan delegate to bookings via the BookingCreator/
// BookingCanceller interfaces (service.go) rather than duplicating
// capacity-enforcement logic that already lives in internal/bookings.
package plans

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/eventbus"
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

// Create publishes the plan immediately. The proto has no separate
// PublishPlan RPC, so a 'draft' default would be a dead end — nothing could
// ever move it to 'published', and SearchPlans/GetHomeFeed/GetNearbyPlans/
// GetRecommendedPlans all filter on status='published'.
func (r *Repository) Create(ctx context.Context, p *Plan) (*Plan, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO plans (title, description, category_id, host_id, city_id, venue_id,
			starts_at, ends_at, capacity, price_minor, currency, status, location)
		VALUES ($1, $2, NULLIF($3,'')::uuid, $4::uuid, NULLIF($5,'')::uuid, NULLIF($6,'')::uuid,
			$7, $8, $9, $10, $11, 'published',
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

type SearchFilter struct {
	CityID     string
	CategoryID string
	Latitude   *float64
	Longitude  *float64
	RadiusKM   float64
}

func (r *Repository) Search(ctx context.Context, f SearchFilter, limit int) ([]*Plan, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, title, description, COALESCE(category_id::text,''), host_id::text,
			COALESCE(city_id::text,''), COALESCE(venue_id::text,''), starts_at, ends_at,
			capacity, confirmed_count, price_minor, currency, status,
			ST_Y(location::geometry), ST_X(location::geometry), created_at, updated_at
		FROM plans
		WHERE status = 'published'
		  AND (city_id = NULLIF($1,'')::uuid OR $1 = '')
		  AND (category_id = NULLIF($2,'')::uuid OR $2 = '')
		  AND ($3::float8 IS NULL OR ST_DWithin(location,
		       ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography, $5 * 1000))
		ORDER BY starts_at
		LIMIT $6`,
		f.CityID, f.CategoryID, f.Latitude, f.Longitude, f.RadiusKM, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []*Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Title, &p.Description, &p.CategoryID, &p.HostID,
			&p.CityID, &p.VenueID, &p.StartsAt, &p.EndsAt, &p.Capacity, &p.ConfirmedCount,
			&p.PriceMinor, &p.Currency, &p.Status, &p.Latitude, &p.Longitude,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		plans = append(plans, &p)
	}
	return plans, rows.Err()
}

type cancelPayload struct {
	PlanID string `json:"plan_id"`
	Reason string `json:"reason"`
}

// Cancel sets the plan cancelled and writes a single PLAN_CANCELLED outbox
// event in the same transaction — cmd/worker fans that out to per-booking
// cancellation/refund evaluation rather than this repository looping over
// bookings itself (keeps the plans/bookings packages decoupled).
func (r *Repository) Cancel(ctx context.Context, planID, reason string) (*Plan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var p Plan
	p.ID = planID
	if err := tx.QueryRow(ctx, `
		UPDATE plans SET status = 'cancelled', updated_at = now()
		WHERE id = $1
		RETURNING title, description, COALESCE(category_id::text,''), host_id::text,
			COALESCE(city_id::text,''), COALESCE(venue_id::text,''), starts_at, ends_at,
			capacity, confirmed_count, price_minor, currency, status,
			ST_Y(location::geometry), ST_X(location::geometry), created_at, updated_at`,
		planID,
	).Scan(&p.Title, &p.Description, &p.CategoryID, &p.HostID, &p.CityID, &p.VenueID,
		&p.StartsAt, &p.EndsAt, &p.Capacity, &p.ConfirmedCount, &p.PriceMinor, &p.Currency,
		&p.Status, &p.Latitude, &p.Longitude, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(cancelPayload{PlanID: planID, Reason: reason})
	if err != nil {
		return nil, err
	}
	if err := eventbus.Enqueue(ctx, tx, "PLAN_CANCELLED", planID, payload); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}
