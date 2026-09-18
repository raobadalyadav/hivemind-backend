// Package discovery implements PRD §13.3 Discovery — every RPC is fully
// implemented. GetNearbyPlans uses PostGIS ST_DWithin (PRD §20 MVP row).
// GetHomeFeed sections (TODAY/TONIGHT/WEEKEND/NEAR_YOU/FOR_YOU) are all
// variations on "upcoming published plans in the user's city" — NEAR_YOU and
// FOR_YOU currently behave the same, since GetHomeFeedRequest carries no geo
// origin (only GetNearbyPlansRequest does) and no behavioral ranking data
// exists yet (see internal/recommendation's same cold-start note).
package discovery

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

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

// dateFilter returns the SQL fragment (and whether it applies) for a home
// feed section. TODAY/TONIGHT/WEEKEND are server-local date arithmetic —
// ponytail: no per-user timezone handling yet, add when cities outside one
// timezone matter.
func dateFilter(section string) string {
	switch section {
	case "TODAY":
		return "p.starts_at::date = current_date"
	case "TONIGHT":
		return "p.starts_at::date = current_date AND p.starts_at::time >= '18:00'"
	case "WEEKEND":
		return "p.starts_at::date IN (date_trunc('week', now()) + interval '5 days', date_trunc('week', now()) + interval '6 days')"
	default: // NEAR_YOU, FOR_YOU: no date narrowing, just upcoming
		return "p.starts_at > now()"
	}
}

func (r *Repository) HomeFeedPlanIDs(ctx context.Context, userID, section string, limit int) ([]string, error) {
	query := `
		SELECT p.id FROM plans p
		WHERE p.status = 'published' AND ` + dateFilter(section) + `
		  AND (p.city_id = (SELECT city_id FROM users WHERE id = $1) OR (SELECT city_id FROM users WHERE id = $1) IS NULL)
		ORDER BY p.starts_at
		LIMIT $2`

	rows, err := r.pool.Query(ctx, query, userID, limit)
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
