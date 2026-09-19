// Package admin implements PRD §13.18 Admin & Analytics / §23 — every RPC is
// fully implemented. SuspendUser/OverrideBookingStatus demonstrate the
// "every moderation/financial override requires permission and produces an
// audit log" rule from PRD §31 Definition of Done; RBAC itself is enforced
// by pkg/grpcmiddleware's adminMethods gate, not here.
package admin

import (
	"context"
	"encoding/json"
	"time"

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

// ApproveHost flips the user's role and activates their payout account in
// one transaction, same audit discipline as SuspendUser.
func (r *Repository) ApproveHost(ctx context.Context, userID, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'host'::user_role WHERE id = $1`, userID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payout_accounts SET status = 'active'::payout_account_status WHERE host_id = $1`, userID,
	); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES (NULLIF($1,'')::uuid, 'approve_host', 'user', $2::uuid, '{}')`,
		actorID, userID,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// MarkPayoutProcessed records that a payout was actually transferred — no
// real bank-transfer API call happens here (see host.Service.RequestPayout's
// comment); an operator calls this once money has genuinely moved.
func (r *Repository) MarkPayoutProcessed(ctx context.Context, payoutID, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE payouts SET status = 'processed'::payout_status WHERE id = $1`, payoutID,
	); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES (NULLIF($1,'')::uuid, 'mark_payout_processed', 'payout', $2::uuid, '{}')`,
		actorID, payoutID,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GrantCredit inserts a positive credit_ledger row — platform-granted,
// non-withdrawable, only ever usable as a checkout discount (PRD §21).
func (r *Repository) GrantCredit(ctx context.Context, userID string, amountMinor int64, reason, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO credit_ledger (user_id, amount_minor, reason) VALUES ($1, $2, $3)`,
		userID, amountMinor, reason,
	); err != nil {
		return err
	}

	details, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES (NULLIF($1,'')::uuid, 'grant_credit', 'user', $2::uuid, $3)`,
		actorID, userID, details,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type Coupon struct {
	ID            string
	Code          string
	DiscountType  string
	DiscountValue int64
	MaxUses       int32
	UsesCount     int32
	Active        bool
}

func (r *Repository) CreateCoupon(ctx context.Context, code, discountType string, discountValue int64, maxUses int32, expiresAt *time.Time) (*Coupon, error) {
	c := &Coupon{Code: code, DiscountType: discountType, DiscountValue: discountValue, MaxUses: maxUses, Active: true}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO promo_codes (code, discount_type, discount_value, max_uses, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, uses_count`,
		code, discountType, discountValue, maxUses, expiresAt,
	).Scan(&c.ID, &c.UsesCount)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (r *Repository) ListCoupons(ctx context.Context, limit int) ([]*Coupon, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, code, discount_type, discount_value, max_uses, uses_count, active
		FROM promo_codes ORDER BY created_at DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Coupon
	for rows.Next() {
		var c Coupon
		if err := rows.Scan(&c.ID, &c.Code, &c.DiscountType, &c.DiscountValue, &c.MaxUses, &c.UsesCount, &c.Active); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *Repository) DeactivateCoupon(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE promo_codes SET active = false WHERE id = $1`, id)
	return err
}

type City struct {
	ID      string
	Name    string
	State   string
	Country string
	Status  string
}

// CreateCity writes the city and its audit_logs row in one transaction —
// same shape as ApproveHost/GrantCredit. country defaults to 'IN' when
// empty, matching the column's own DB default.
func (r *Repository) CreateCity(ctx context.Context, name, state, country, actorID string) (*City, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	c := &City{Name: name, State: state, Country: country}
	if err := tx.QueryRow(ctx, `
		INSERT INTO cities (name, state, country)
		VALUES ($1, $2, COALESCE(NULLIF($3,''), 'IN'))
		RETURNING id, state, country, status::text`,
		name, state, country,
	).Scan(&c.ID, &c.State, &c.Country, &c.Status); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES (NULLIF($1,'')::uuid, 'create_city', 'city', $2::uuid, '{}')`,
		actorID, c.ID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateCityStatus transitions a city through flow.md §55's launch
// lifecycle (pre_launch → soft_launch → active → scaling → mature) — no
// ordering enforced here (an operator may need to roll a city back to
// soft_launch), same as OverrideBookingStatus not restricting transitions.
func (r *Repository) UpdateCityStatus(ctx context.Context, cityID, newStatus, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE cities SET status = $2::city_status WHERE id = $1`, cityID, newStatus,
	); err != nil {
		return err
	}

	details, err := json.Marshal(map[string]string{"new_status": newStatus})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES (NULLIF($1,'')::uuid, 'update_city_status', 'city', $2::uuid, $3)`,
		actorID, cityID, details,
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
