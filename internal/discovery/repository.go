// Package discovery implements PRD §13.3 Discovery — every RPC is fully
// implemented. GetNearbyPlans uses PostGIS ST_DWithin (PRD §20 MVP row).
// GetHomeFeed sections (TODAY/TONIGHT/WEEKEND/NEAR_YOU/FOR_YOU): TODAY/
// TONIGHT/WEEKEND are computed in each plan's own city timezone (not
// server-local) so the same code is correct for a city in any timezone.
// NEAR_YOU uses the user's last known device location (see
// UserService.UpdateLocation) when available, falling back to city-only.
// FOR_YOU has no behavioral ranking data yet (see internal/recommendation's
// same cold-start note), so it's still city-only.
package discovery

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrCityNotFound = errors.New("discovery: no city found near that location")

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) NearbyPlanIDs(ctx context.Context, lat, lng, radiusKM float64, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM plans
		WHERE status = 'published'
		  AND ST_DWithin(location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
		ORDER BY location <-> ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography
		LIMIT $4`,
		lng, lat, radiusKM*1000, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DetectCity resolves a GPS point to the nearest launched city (flow.md
// §2.3's "ask location permission → show nearby plans" onboarding step).
func (r *Repository) DetectCity(ctx context.Context, lat, lng float64) (id, name string, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT id, name FROM cities
		WHERE active AND centroid IS NOT NULL
		ORDER BY centroid <-> ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography
		LIMIT 1`,
		lng, lat,
	).Scan(&id, &name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrCityNotFound
		}
		return "", "", err
	}
	return id, name, nil
}

// dateFilter returns the SQL fragment for a home feed section's date
// narrowing, computed in the plan's own city timezone (falling back to UTC
// for a plan whose city has none set) — not the server's timezone.
func dateFilter(section string) string {
	const tz = "COALESCE(c.timezone, 'UTC')"
	localDate := "(p.starts_at AT TIME ZONE " + tz + ")::date"
	localTime := "(p.starts_at AT TIME ZONE " + tz + ")::time"
	todayLocal := "(now() AT TIME ZONE " + tz + ")::date"

	switch section {
	case "TODAY":
		return localDate + " = " + todayLocal
	case "TONIGHT":
		return localDate + " = " + todayLocal + " AND " + localTime + " >= '18:00'"
	case "WEEKEND":
		return localDate + " IN (" +
			"(date_trunc('week', now() AT TIME ZONE " + tz + ") + interval '5 days')::date, " +
			"(date_trunc('week', now() AT TIME ZONE " + tz + ") + interval '6 days')::date)"
	default: // NEAR_YOU, FOR_YOU: no date narrowing, just upcoming
		return "p.starts_at > now()"
	}
}

// travelCityID (flow.md §54 Travel Mode): optional preview of a city other
// than the caller's home city_id. Applies to TODAY/TONIGHT/WEEKEND/FOR_YOU
// only — NEAR_YOU is device-location-based and stays untouched, matching
// the same NEAR_YOU carve-out internal/recommendation uses.
func (r *Repository) HomeFeedPlanIDs(ctx context.Context, userID, section, travelCityID string, limit int) ([]string, error) {
	if section == "NEAR_YOU" {
		ids, usedLocation, err := r.nearYouPlanIDs(ctx, userID, limit)
		if err != nil {
			return nil, err
		}
		if usedLocation {
			return ids, nil
		}
		// user has no last_location yet — fall through to the city-only query below
	}

	query := `
		SELECT p.id FROM plans p
		LEFT JOIN cities c ON c.id = p.city_id
		WHERE p.status = 'published' AND ` + dateFilter(section) + `
		  AND (p.city_id = COALESCE(NULLIF($3,'')::uuid, (SELECT city_id FROM users WHERE id = $1))
		       OR COALESCE(NULLIF($3,'')::uuid, (SELECT city_id FROM users WHERE id = $1)) IS NULL)
		ORDER BY
		  (EXISTS(SELECT 1 FROM promoted_listings pl WHERE pl.plan_id = p.id
		     AND pl.status = 'paid'::promoted_listing_status AND now() BETWEEN pl.starts_at AND pl.ends_at)) DESC,
		  p.starts_at
		LIMIT $2`

	rows, err := r.pool.Query(ctx, query, userID, limit, travelCityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// nearYouPlanIDs uses the user's last known device location when set.
// usedLocation is false when the user has no last_location yet, telling the
// caller to fall back to the city-only query instead.
func (r *Repository) nearYouPlanIDs(ctx context.Context, userID string, limit int) (ids []string, usedLocation bool, err error) {
	var hasLocation bool
	if err := r.pool.QueryRow(ctx,
		`SELECT last_location IS NOT NULL FROM users WHERE id = $1`, userID,
	).Scan(&hasLocation); err != nil {
		return nil, false, err
	}
	if !hasLocation {
		return nil, false, nil
	}

	const defaultRadiusKM = 15.0
	rows, err := r.pool.Query(ctx, `
		SELECT p.id FROM plans p, users u
		WHERE u.id = $1 AND p.status = 'published' AND p.starts_at > now()
		  AND ST_DWithin(p.location, u.last_location, $2 * 1000)
		ORDER BY p.location <-> u.last_location
		LIMIT $3`,
		userID, defaultRadiusKM, limit,
	)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	return ids, true, rows.Err()
}
