// Package admin implements PRD §13.18 Admin & Analytics / §23 — every RPC is
// fully implemented. SuspendUser/OverrideBookingStatus demonstrate the
// "every moderation/financial override requires permission and produces an
// audit log" rule from PRD §31 Definition of Done; RBAC itself is enforced
// by pkg/grpcmiddleware's adminMethods gate, not here.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/eventbus"
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

type SOSEvent struct {
	ID, UserID, UserName, PlanID string
	Lat, Lng                     *float64
	Note, Delivery, DeliveryErr  string
	Acknowledged                 bool
	CreatedAt                    time.Time
}

func (r *Repository) ListSOSEvents(ctx context.Context, onlyOpen bool, limit int) ([]SOSEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT e.id::text, e.user_id::text, COALESCE(up.display_name,''), COALESCE(e.plan_id::text,''),
			e.latitude, e.longitude, e.note, e.contact_delivery, e.delivery_error, e.acknowledged_at IS NOT NULL, e.created_at
		FROM sos_events e LEFT JOIN user_profiles up ON up.user_id = e.user_id
		WHERE (NOT $1 OR e.acknowledged_at IS NULL)
		ORDER BY e.created_at DESC LIMIT $2`, onlyOpen, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SOSEvent
	for rows.Next() {
		var e SOSEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.UserName, &e.PlanID, &e.Lat, &e.Lng, &e.Note,
			&e.Delivery, &e.DeliveryErr, &e.Acknowledged, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AcknowledgeSOSEvent stamps the first acknowledgement only (the WHERE keeps
// a later admin from overwriting who handled it) and audits it in the same tx.
func (r *Repository) AcknowledgeSOSEvent(ctx context.Context, sosID, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE sos_events SET acknowledged_by = $2, acknowledged_at = now()
		WHERE id = $1 AND acknowledged_at IS NULL`, sosID, actorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
			VALUES ($1, 'acknowledge_sos', 'sos_event', $2, '{}')`, actorID, sosID); err != nil {
			return err
		}
	} else {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sos_events WHERE id = $1)`, sosID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return tx.Commit(ctx)
}

type ExternalEvent struct {
	ID, CityID, CategoryID, Title, Description, Source, SourceURL, VenueName, ImageURL string
	StartsAt                                                                           time.Time
	EndsAt                                                                             *time.Time
}

func (r *Repository) CreateExternalEvent(ctx context.Context, e ExternalEvent, actorID string) (*ExternalEvent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	out := e
	if err := tx.QueryRow(ctx, `
		INSERT INTO external_events (city_id, category_id, title, description, source, source_url, venue_name, image_url,
			starts_at, ends_at, created_by)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11::uuid)
		RETURNING id::text`,
		e.CityID, e.CategoryID, e.Title, e.Description, e.Source, e.SourceURL, e.VenueName, e.ImageURL,
		e.StartsAt, e.EndsAt, actorID).Scan(&out.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES ($1, 'create_external_event', 'external_event', $2, jsonb_build_object('title', $3::text))`,
		actorID, out.ID, e.Title); err != nil {
		return nil, err
	}
	return &out, tx.Commit(ctx)
}

func (r *Repository) DeactivateExternalEvent(ctx context.Context, eventID, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var wasActive bool
	err = tx.QueryRow(ctx, `
		UPDATE external_events x SET active = false
		FROM (SELECT id, active FROM external_events WHERE id = $1 FOR UPDATE) old
		WHERE x.id = old.id RETURNING old.active`, eventID).Scan(&wasActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if wasActive {
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
			VALUES ($1, 'deactivate_external_event', 'external_event', $2, '{}')`, actorID, eventID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type VerificationItem struct {
	ID, UserID, UserName, Challenge, ObjectKey string
	CreatedAt                                  time.Time
}

// ListVerificationRequests: pending selfies, oldest first (review queue).
func (r *Repository) ListVerificationRequests(ctx context.Context, limit int) ([]VerificationItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT v.id::text, v.user_id::text, COALESCE(up.display_name, ''), v.challenge, COALESCE(m.object_key, ''), v.created_at
		FROM verification_requests v
		LEFT JOIN user_profiles up ON up.user_id = v.user_id
		LEFT JOIN media_uploads m ON m.id = v.media_id
		WHERE v.status = 'pending' ORDER BY v.created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VerificationItem
	for rows.Next() {
		var v VerificationItem
		if err := rows.Scan(&v.ID, &v.UserID, &v.UserName, &v.Challenge, &v.ObjectKey, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReviewVerification decides a pending request once. The selfie is detached
// (media_id NULL) so the media GC deletes it; approval sets the blue tick.
// Decision, tick, audit row and the user's notification are one transaction.
func (r *Repository) ReviewVerification(ctx context.Context, requestID string, approve bool, reason, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status := "rejected"
	if approve {
		status = "approved"
	}
	var userID string
	err = tx.QueryRow(ctx, `
		UPDATE verification_requests SET status = $2, reject_reason = $3, reviewed_by = $4::uuid, reviewed_at = now(), media_id = NULL
		WHERE id = $1 AND status = 'pending' RETURNING user_id::text`, requestID, status, reason, actorID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	title, body := "You're verified ✓", "Your blue tick is now on your profile."
	if approve {
		if _, err := tx.Exec(ctx, `UPDATE user_profiles SET selfie_verified_at = now() WHERE user_id = $1`, userID); err != nil {
			return err
		}
	} else {
		title, body = "Verification not approved", "Your selfie didn't pass review. You can try again."
		if reason != "" {
			body = reason + " You can try again."
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, subject_type, subject_id, details)
		VALUES ($1, $2, 'user', $3::uuid, jsonb_build_object('request_id', $4::text, 'reason', $5::text))`,
		actorID, "verification_"+status, userID, requestID, reason); err != nil {
		return err
	}
	if err := eventbus.EnqueueNotifyUser(ctx, tx, eventbus.NotifyUserPayload{
		UserID: userID, Title: title, Body: body, DeepLink: "hivemind://verification", Channel: "push"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
