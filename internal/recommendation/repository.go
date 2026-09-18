// Package recommendation implements PRD §11/§20. GetRecommendedPlans is the
// fully working vertical slice, but only as the PRD §20 MVP row describes it:
// "Cold start: Editorial/curated plans by city" — upcoming published plans,
// no personalization weighting yet. The full ranking formula in PRD §20
// ("0.30*distance + 0.25*interest_match + ...") and GetSmartMatch are
// TODO(phase4) — they need behavioral data this scaffold has no way to
// collect yet.
package recommendation

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

// UpcomingPlanIDs returns editorial/curated upcoming plans in the user's
// city (falls back to all cities if the user has none set).
func (r *Repository) UpcomingPlanIDs(ctx context.Context, userID string, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id FROM plans p
		WHERE p.status = 'published' AND p.starts_at > now()
		  AND (p.city_id = (SELECT city_id FROM users WHERE id = $1) OR (SELECT city_id FROM users WHERE id = $1) IS NULL)
		ORDER BY p.starts_at
		LIMIT $2`,
		userID, limit,
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
