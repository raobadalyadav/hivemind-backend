// Package search implements PRD §20 Search, Geospatial and Recommendation
// Design. SearchText is the fully working vertical slice, backed by
// PostgreSQL ILIKE (upgrade path: trigram/FTS index, then OpenSearch per the
// PRD §20 MVP-vs-later table — this scaffold keeps the query simple).
package search

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

func (r *Repository) SearchPlanIDs(ctx context.Context, query, cityID string, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM plans
		WHERE status = 'published'
		  AND (city_id = NULLIF($2,'')::uuid OR $2 = '')
		  AND (title ILIKE '%' || $1 || '%' OR description ILIKE '%' || $1 || '%')
		ORDER BY
		  (EXISTS(SELECT 1 FROM promoted_listings pl WHERE pl.plan_id = plans.id
		     AND pl.status = 'paid'::promoted_listing_status AND now() BETWEEN pl.starts_at AND pl.ends_at)) DESC,
		  starts_at
		LIMIT $3`,
		query, cityID, limit,
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
