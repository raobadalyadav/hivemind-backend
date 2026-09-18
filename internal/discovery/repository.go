// Package discovery implements PRD §13.3 Discovery. GetNearbyPlans is the
// fully working vertical slice (PostGIS ST_DWithin, per PRD §20 MVP row
// "Nearby plans: PostGIS ST_DWithin + distance sort"); GetHomeFeed
// (today/tonight/weekend/for-you sectioning) is a typed stub.
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
