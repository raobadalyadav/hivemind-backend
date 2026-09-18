// Package admin implements PRD §13.18 Admin & Analytics / §23. ListUsers/
// SuspendUser are the fully working vertical slice — SuspendUser
// demonstrates the "every moderation/financial override requires permission
// and produces an audit log" rule from PRD §31 Definition of Done.
// ListReports/OverrideBookingStatus/GetDashboardStats are typed stubs.
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
