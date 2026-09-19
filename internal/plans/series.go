package plans

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const seriesHorizon = 60 * 24 * time.Hour

// ExtendSeries materialises upcoming occurrences of a recurring plan as real
// plan rows sharing series_id. Idempotent: ON CONFLICT (series_id, starts_at)
// DO NOTHING makes repeated calls (create, hourly job, retries) harmless.
func (r *Repository) ExtendSeries(ctx context.Context, templateID string, now time.Time) (int, error) {
	var ruleText string
	var active bool
	var endedBefore *time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT recurrence_rule, active, ended_before FROM plan_recurrences WHERE plan_id = $1`, templateID,
	).Scan(&ruleText, &active, &endedBefore)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rule, err := ParseRule(ruleText)
	if err != nil {
		return 0, err
	}

	var startsAt, endsAt time.Time
	var tz string
	if err := r.pool.QueryRow(ctx, `
		SELECT p.starts_at, p.ends_at, COALESCE(c.timezone, 'UTC')
		FROM plans p LEFT JOIN cities c ON c.id = p.city_id WHERE p.id = $1`, templateID,
	).Scan(&startsAt, &endsAt, &tz); err != nil {
		return 0, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	duration := endsAt.Sub(startsAt)

	created := 0
	for _, start := range rule.Occurrences(startsAt, loc, now.Add(seriesHorizon)) {
		if endedBefore != nil && !start.Before(*endedBefore) {
			continue
		}
		tag, err := r.pool.Exec(ctx, `
			INSERT INTO plans (title, description, category_id, host_id, city_id, venue_id,
				starts_at, ends_at, capacity, price_minor, currency, status, location,
				join_mode, visibility, community_id, requires_entitlement, series_id)
			SELECT title, description, category_id, host_id, city_id, venue_id,
				$2::timestamptz, $3::timestamptz, capacity, price_minor, currency, 'published', location,
				join_mode, visibility, community_id, requires_entitlement, series_id
			FROM plans WHERE id = $1
			ON CONFLICT (series_id, starts_at) DO NOTHING`,
			templateID, start, start.Add(duration),
		)
		if err != nil {
			return created, err
		}
		created += int(tag.RowsAffected())
	}

	// Community plans list their occurrences under the community too.
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO community_events (community_id, plan_id)
		SELECT community_id, id FROM plans WHERE series_id = $1 AND community_id IS NOT NULL
		ON CONFLICT DO NOTHING`, templateID,
	); err != nil {
		return created, err
	}
	return created, nil
}

// ExtendAllSeries is the hourly worker job: top up every active series.
func (r *Repository) ExtendAllSeries(ctx context.Context, now time.Time) (int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT plan_id::text FROM plan_recurrences WHERE active AND ended_before IS NULL`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		n, err := r.ExtendSeries(ctx, id, now)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// FutureOccurrenceIDs lists this plan's series occurrences at or after the
// given plan's start that are still published (the plan itself included).
func (r *Repository) FutureOccurrenceIDs(ctx context.Context, p *Plan) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text FROM plans
		WHERE series_id = NULLIF($1,'')::uuid AND starts_at >= $2 AND status = 'published'
		ORDER BY starts_at`, p.SeriesID, p.StartsAt)
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

// EndSeriesFrom stops the series generating anything at or after t.
func (r *Repository) EndSeriesFrom(ctx context.Context, seriesID string, t time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE plan_recurrences SET ended_before = LEAST(COALESCE(ended_before, $2::timestamptz), $2::timestamptz)
		WHERE plan_id = $1`, seriesID, t)
	return err
}
