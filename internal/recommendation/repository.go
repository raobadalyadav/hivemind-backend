// Package recommendation implements PRD §11/§20 — every RPC is fully
// implemented. GetRecommendedPlans now applies PRD §20's full ranking
// formula (0.30*distance + 0.25*interest_match + 0.15*time_fit +
// 0.10*capacity_fit + 0.10*quality + 0.10*personalization), computed live
// in one query — no cached score column, same convention as avg_rating
// elsewhere. GetSmartMatch/GetPeopleRecommendations are PRD §20's "People
// matching: Interest/intent overlap" row — pure SQL interest-overlap
// ranking, no ML service, matching the PRD's explicit rule-based-first
// non-goal for this phase.
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

// UpcomingPlanIDs ranks upcoming published plans in the user's city (falls
// back to all cities if unset) by PRD §20's weighted formula. Weights and
// the two curve constants (50km distance cutoff, 48h/336h time-fit peak
// and decay) are inline per the PRD's own six named terms — a Go-side
// constant table would be over-engineering for six numbers used in exactly
// one query.
//
// interest_match is necessarily binary (category name present in the
// user's interests, case-insensitive), not a proportional overlap — plans
// carry one category, not a tag set.
// ponytail: upgrade to fractional overlap if plan tags are ever added.
// travelCityID (flow.md §54 Travel Mode), when non-empty, previews a city
// other than the caller's home city_id — a request-scoped override only;
// users.city_id itself is never written. Distance-fit still scores against
// the caller's real last_location, since previewing a city means "what
// ranks well there," not faking GPS presence.
func (r *Repository) UpcomingPlanIDs(ctx context.Context, userID, travelCityID string, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		WITH me AS (
			SELECT COALESCE(NULLIF($3,'')::uuid, u.city_id) AS city_id, u.last_location, COALESCE(up.interests, '{}') AS interests
			FROM users u
			LEFT JOIN user_profiles up ON up.user_id = u.id
			WHERE u.id = $1
		),
		candidates AS (
			SELECT p.id, p.host_id, p.category_id, p.starts_at, p.capacity, p.confirmed_count, p.location,
				c.name AS category_name
			FROM plans p
			LEFT JOIN categories c ON c.id = p.category_id
			WHERE p.status = 'published' AND p.starts_at > now()
			  AND (p.city_id = (SELECT city_id FROM me) OR (SELECT city_id FROM me) IS NULL)
		)
		SELECT cd.id,
			(0.30 * COALESCE(GREATEST(0, 1 - ST_Distance(cd.location, me.last_location) / 50000.0), 0.5))
			+
			(0.25 * CASE WHEN cd.category_name IS NOT NULL AND EXISTS (
				SELECT 1 FROM unnest(me.interests) i WHERE cd.category_name ILIKE i
			) THEN 1 ELSE 0 END)
			+
			(0.15 * GREATEST(0, 1 - ABS(EXTRACT(EPOCH FROM (cd.starts_at - now()))/3600 - 48)/336))
			+
			(0.10 * (1 - LEAST(1, ABS(cd.confirmed_count::float / NULLIF(cd.capacity,0) - 0.6) / 0.6)))
			+
			(0.10 * (COALESCE((SELECT AVG(rv.rating) FROM reviews rv JOIN plans p2 ON p2.id = rv.plan_id WHERE p2.host_id = cd.host_id), 3.0) / 5.0))
			+
			(0.10 * LEAST(1.0, (SELECT COUNT(*)::float FROM bookings b
				WHERE b.user_id = $1 AND b.status IN ('confirmed'::booking_status,'attended'::booking_status)
				  AND b.plan_id IN (SELECT id FROM plans WHERE category_id = cd.category_id)) / 5.0))
			AS score
		FROM candidates cd, me
		ORDER BY score DESC
		LIMIT $2`,
		userID, limit, travelCityID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PeopleRecommendationUserIDs ranks other users in the caller's city by
// shared interests (same unnest/INTERSECT technique as SmartMatchUserIDs,
// scoped city-wide instead of per-plan), excluding both-direction blocks.
// Users with 3+ reports against them (an "under review" trust signal — see
// internal/moderation's DeriveBadges) are sorted after everyone else
// rather than excluded outright, since a report alone isn't a finding.
// travelCityID: see UpcomingPlanIDs — same request-scoped preview override.
func (r *Repository) PeopleRecommendationUserIDs(ctx context.Context, callerID, travelCityID string, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id
		FROM users u
		JOIN user_profiles up ON up.user_id = u.id
		WHERE u.id != $1
		  AND u.city_id = COALESCE(NULLIF($3,'')::uuid, (SELECT city_id FROM users WHERE id = $1))
		  AND NOT EXISTS (
			SELECT 1 FROM blocks
			WHERE (user_id = $1 AND blocked_user_id = u.id) OR (user_id = u.id AND blocked_user_id = $1)
		  )
		ORDER BY
			(SELECT COUNT(*) FROM reports WHERE subject_type = 'user' AND subject_id = u.id) >= 3,
			cardinality(ARRAY(
				SELECT unnest(up.interests)
				INTERSECT
				SELECT unnest((SELECT interests FROM user_profiles WHERE user_id = $1))
			)) DESC
		LIMIT $2`,
		callerID, limit, travelCityID,
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
