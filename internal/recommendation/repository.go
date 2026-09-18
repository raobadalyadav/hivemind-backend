// Package recommendation implements PRD §11/§20 — every RPC is fully
// implemented, at the depth the data actually collected supports.
// GetRecommendedPlans is PRD §20's stated MVP row: "Cold start:
// Editorial/curated plans by city" — upcoming published plans, no
// personalization weighting. GetSmartMatch is PRD §20's other MVP row:
// "People matching: Interest/intent overlap" — ranks a plan's confirmed
// participants by shared user_profiles.interests, pure SQL, no ML service.
// The full plan-ranking formula in PRD §20 ("0.30*distance +
// 0.25*interest_match + ...") stays a TODO(phase4) — those weight terms
// (distance/time_fit/personalization) need behavioral data this scaffold
// doesn't collect yet, and apply to ranking plans, not this per-plan people
// match.
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

// SmartMatchUserIDs ranks a plan's other confirmed participants by shared
// interests with the caller — the intersection is computed via
// unnest/INTERSECT since Postgres core has no built-in text[] intersection
// operator; cardinality() of that gives the overlap count to sort by.
func (r *Repository) SmartMatchUserIDs(ctx context.Context, callerID, planID string, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT pp.user_id
		FROM plan_participants pp
		JOIN user_profiles up ON up.user_id = pp.user_id
		WHERE pp.plan_id = $2 AND pp.status = 'confirmed' AND pp.user_id != $1
		ORDER BY cardinality(ARRAY(
			SELECT unnest(up.interests)
			INTERSECT
			SELECT unnest((SELECT interests FROM user_profiles WHERE user_id = $1))
		)) DESC
		LIMIT $3`,
		callerID, planID, limit,
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
