// Package admin implements PRD §13.18 Admin & Analytics / §23 — every RPC is
// fully implemented. SuspendUser/OverrideBookingStatus demonstrate the
// "every moderation/financial override requires permission and produces an
// audit log" rule from PRD §31 Definition of Done; RBAC itself is enforced
// by pkg/grpcmiddleware's adminMethods gate, not here.
package admin

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) ListUsers(ctx context.Context, query string, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM users
		WHERE $1 = '' OR email ILIKE '%' || $1 || '%'
		ORDER BY created_at DESC LIMIT $2`, query, limit,
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

// SuspendUser flips the user's status and writes an audit_logs row in the
// same transaction — suspension without an audit trail should be impossible.
func (r *Repository) SuspendUser(ctx context.Context, userID, reason, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET status = 'suspended', updated_at = now() WHERE id = $1`, userID,
	); err != nil {
		return err
	}

	details, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES (NULLIF($1,'')::uuid, 'suspend_user', 'user', $2::uuid, $3)`,
		actorID, userID, details,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) ListReportCaseIDs(ctx context.Context, statusFilter string, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM moderation_cases
		WHERE $1 = '' OR status = $1::case_status
		ORDER BY created_at DESC LIMIT $2`, statusFilter, limit,
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

// OverrideBookingStatus writes the override and its audit_logs row in one
// transaction, same discipline as SuspendUser.
func (r *Repository) OverrideBookingStatus(ctx context.Context, bookingID, newStatus, reason, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE bookings SET status = $2::booking_status, updated_at = now() WHERE id = $1`,
		bookingID, newStatus,
	); err != nil {
		return err
	}

	details, err := json.Marshal(map[string]string{"reason": reason, "new_status": newStatus})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES (NULLIF($1,'')::uuid, 'override_booking_status', 'booking', $2::uuid, $3)`,
		actorID, bookingID, details,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type DashboardStats struct {
	DAU           int64
	MAU           int64
	BookingsToday int64
	GMVMinorUnits int64
}

// GetDashboardStats. DAU/MAU come from pkg/analytics's session_active
// events (written on every sign-in/refresh — see internal/auth's
// issueTokens) — real distinct-user counts, not a TODO(phase2) stub anymore.
func (r *Repository) GetDashboardStats(ctx context.Context, cityID string) (*DashboardStats, error) {
	var stats DashboardStats
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(DISTINCT e.user_id) FROM events e JOIN users u ON u.id = e.user_id
			 WHERE e.event_name = 'session_active' AND e.occurred_at::date = current_date
			   AND (u.city_id = NULLIF($1,'')::uuid OR $1 = '')),
			(SELECT count(DISTINCT e.user_id) FROM events e JOIN users u ON u.id = e.user_id
			 WHERE e.event_name = 'session_active' AND e.occurred_at >= now() - interval '30 days'
			   AND (u.city_id = NULLIF($1,'')::uuid OR $1 = '')),
			(SELECT count(*) FROM bookings b JOIN plans p ON p.id = b.plan_id
			 WHERE b.created_at::date = current_date
			   AND (p.city_id = NULLIF($1,'')::uuid OR $1 = '')),
			(SELECT COALESCE(sum(pay.amount_minor), 0) FROM payments pay
			 JOIN orders o ON o.id = pay.order_id
			 JOIN bookings b ON b.id = o.booking_id
			 JOIN plans p ON p.id = b.plan_id
			 WHERE pay.status = 'captured' AND pay.created_at::date = current_date
			   AND (p.city_id = NULLIF($1,'')::uuid OR $1 = ''))`,
		cityID,
	).Scan(&stats.DAU, &stats.MAU, &stats.BookingsToday, &stats.GMVMinorUnits)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}
