// Package plans implements PRD §13.4 Plans — every RPC is fully implemented.
// JoinPlan/LeavePlan delegate to bookings via the BookingCreator/
// BookingCanceller interfaces (service.go) rather than duplicating
// capacity-enforcement logic that already lives in internal/bookings.
package plans

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/eventbus"
)

var (
	ErrPlanNotFound    = errors.New("plans: plan not found")
	ErrRequestNotFound = errors.New("plans: join request not found")
)

type Plan struct {
	ID                  string
	Title               string
	Description         string
	CategoryID          string
	HostID              string
	CityID              string
	VenueID             string
	StartsAt            time.Time
	EndsAt              time.Time
	Capacity            int32
	ConfirmedCount      int32
	PriceMinor          int64
	Currency            string
	Status              string
	Latitude            *float64
	Longitude           *float64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	JoinMode            string // open | approval | invite_only
	Visibility          string // public | private | community
	CommunityID         string
	RequiresEntitlement string
	SeriesID            string
	CoverMediaID        string // input on create (an upload id)
	CoverURL            string
	CoverThumbURL       string

	// Filled by Decorate (not part of planColumns).
	SavedByMe       bool
	HostRatingAvg   float64
	HostRatingCount int32
}

// planColumns is the single column list every plan read shares — six call
// sites used to each carry their own copy of the scan list.
const planColumns = `id, title, description, COALESCE(category_id::text,''), host_id::text,
	COALESCE(city_id::text,''), COALESCE(venue_id::text,''), starts_at, ends_at,
	capacity, confirmed_count, price_minor, currency, status::text,
	ST_Y(location::geometry), ST_X(location::geometry), created_at, updated_at,
	join_mode::text, visibility::text, COALESCE(community_id::text,''),
	COALESCE(requires_entitlement,''), COALESCE(series_id::text,''),
	cover_url, cover_thumb_url`

type rowScanner interface{ Scan(dest ...any) error }

func scanPlan(row rowScanner, p *Plan) error {
	return row.Scan(&p.ID, &p.Title, &p.Description, &p.CategoryID, &p.HostID,
		&p.CityID, &p.VenueID, &p.StartsAt, &p.EndsAt, &p.Capacity, &p.ConfirmedCount,
		&p.PriceMinor, &p.Currency, &p.Status, &p.Latitude, &p.Longitude,
		&p.CreatedAt, &p.UpdatedAt, &p.JoinMode, &p.Visibility, &p.CommunityID,
		&p.RequiresEntitlement, &p.SeriesID, &p.CoverURL, &p.CoverThumbURL)
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create publishes the plan immediately (the proto has no separate
// PublishPlan RPC). A community plan also gets its community_events row in
// the same transaction; a recurring plan becomes its own series template
// (series_id = id) with its rule stored in plan_recurrences.
func (r *Repository) Create(ctx context.Context, p *Plan, recurrenceRule string) (*Plan, error) {
	if p.JoinMode == "" {
		p.JoinMode = "open"
	}
	if p.Visibility == "" {
		p.Visibility = "public"
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO plans (title, description, category_id, host_id, city_id, venue_id,
			starts_at, ends_at, capacity, price_minor, currency, status, location,
			join_mode, visibility, community_id, requires_entitlement, cover_media_id, cover_url, cover_thumb_url)
		VALUES ($1, $2, NULLIF($3,'')::uuid, $4::uuid, NULLIF($5,'')::uuid, NULLIF($6,'')::uuid,
			$7, $8, $9, $10, $11, 'published',
			CASE WHEN $12::float8 IS NULL THEN NULL
			     ELSE ST_SetSRID(ST_MakePoint($13, $12), 4326)::geography END,
			$14::plan_join_mode, $15::plan_visibility, NULLIF($16,'')::uuid, NULLIF($17,''),
			NULLIF($18,'')::uuid, $19, $20)
		RETURNING id::text`,
		p.Title, p.Description, p.CategoryID, p.HostID, p.CityID, p.VenueID,
		p.StartsAt, p.EndsAt, p.Capacity, p.PriceMinor, p.Currency,
		p.Latitude, p.Longitude,
		p.JoinMode, p.Visibility, p.CommunityID, p.RequiresEntitlement,
		p.CoverMediaID, p.CoverURL, p.CoverThumbURL,
	).Scan(&id)
	if err != nil {
		return nil, err
	}

	if p.CommunityID != "" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO community_events (community_id, plan_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			p.CommunityID, id,
		); err != nil {
			return nil, err
		}
	}
	if recurrenceRule != "" {
		if _, err := tx.Exec(ctx, `UPDATE plans SET series_id = id WHERE id = $1`, id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO plan_recurrences (plan_id, recurrence_rule) VALUES ($1, $2)`, id, recurrenceRule,
		); err != nil {
			return nil, err
		}
	}

	var out Plan
	if err := scanPlan(tx.QueryRow(ctx, `SELECT `+planColumns+` FROM plans WHERE id = $1`, id), &out); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCategoryName is used by SuggestPlanDraft — empty string (not an
// error) when categoryID is empty or unknown, since a draft suggestion has
// a sensible generic fallback either way.
func (r *Repository) GetCategoryName(ctx context.Context, categoryID string) (string, error) {
	if categoryID == "" {
		return "", nil
	}
	var name string
	err := r.pool.QueryRow(ctx, `SELECT name FROM categories WHERE id = $1`, categoryID).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return name, nil
}

// EntitlementProductExists guards requires_entitlement: a typo would
// otherwise create a plan nobody can ever join.
func (r *Repository) EntitlementProductExists(ctx context.Context, name string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_products WHERE name = $1)`, name).Scan(&ok)
	return ok, err
}

// Get reads any plan regardless of visibility — internal callers only.
// Handlers must go through Service.GetPlanAsUser.
func (r *Repository) Get(ctx context.Context, id string) (*Plan, error) {
	var p Plan
	if err := scanPlan(r.pool.QueryRow(ctx, `SELECT `+planColumns+` FROM plans WHERE id = $1`, id), &p); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	return &p, nil
}

// CanView decides whether userID may see a non-public plan (public plans
// never call this). Private: invited, has requested to join, or is a
// participant. Community: additionally, community members.
func (r *Repository) CanView(ctx context.Context, p *Plan, userID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM plan_invites WHERE plan_id = $1 AND user_id = $2)
		    OR EXISTS(SELECT 1 FROM plan_join_requests WHERE plan_id = $1 AND user_id = $2 AND status IN ('pending','approved'))
		    OR EXISTS(SELECT 1 FROM plan_participants WHERE plan_id = $1 AND user_id = $2 AND status = 'confirmed')
		    OR ($3::text = 'community' AND $4::text <> '' AND EXISTS(
		         SELECT 1 FROM community_members WHERE community_id = NULLIF($4,'')::uuid AND user_id = $2))`,
		p.ID, userID, p.Visibility, p.CommunityID,
	).Scan(&ok)
	return ok, err
}

type SearchFilter struct {
	CityID     string
	CategoryID string
	Latitude   *float64
	Longitude  *float64
	RadiusKM   float64
	HostID     string
}

// Search reads plans_discoverable (published, public, one upcoming
// occurrence per series) — never raw plans, so private plans can't leak.
func (r *Repository) Search(ctx context.Context, f SearchFilter, limit int) ([]*Plan, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+planColumns+`
		FROM plans_discoverable
		WHERE (city_id = NULLIF($1,'')::uuid OR $1 = '')
		  AND (category_id = NULLIF($2,'')::uuid OR $2 = '')
		  AND ($3::float8 IS NULL OR ST_DWithin(location,
		       ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography, $5 * 1000))
		  AND (host_id = NULLIF($7,'')::uuid OR $7 = '')
		ORDER BY starts_at
		LIMIT $6`,
		f.CityID, f.CategoryID, f.Latitude, f.Longitude, f.RadiusKM, limit, f.HostID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []*Plan
	for rows.Next() {
		var p Plan
		if err := scanPlan(rows, &p); err != nil {
			return nil, err
		}
		plans = append(plans, &p)
	}
	return plans, rows.Err()
}

type cancelPayload struct {
	PlanID string `json:"plan_id"`
	Reason string `json:"reason"`
}

// Cancel sets the plan cancelled and writes a single PLAN_CANCELLED outbox
// event in the same transaction — cmd/worker fans that out to per-booking
// cancellation/refund evaluation rather than this repository looping over
// bookings itself (keeps the plans/bookings packages decoupled).
func (r *Repository) Cancel(ctx context.Context, planID, reason string) (*Plan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var p Plan
	if err := scanPlan(tx.QueryRow(ctx, `
		UPDATE plans SET status = 'cancelled', updated_at = now()
		WHERE id = $1
		RETURNING `+planColumns, planID), &p); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}

	payload, err := json.Marshal(cancelPayload{PlanID: planID, Reason: reason})
	if err != nil {
		return nil, err
	}
	if err := eventbus.Enqueue(ctx, tx, "PLAN_CANCELLED", planID, payload); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

// ---- join requests & invites (flow.md §14) ----

type JoinRequest struct {
	ID        string
	PlanID    string
	UserID    string
	Status    string
	Message   string
	CreatedAt time.Time
}

// CreateJoinRequest is idempotent: a repeat request returns the existing
// row unchanged (a rejected requester stays rejected).
func (r *Repository) CreateJoinRequest(ctx context.Context, planID, userID, message string) (*JoinRequest, error) {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO plan_join_requests (plan_id, user_id, message) VALUES ($1, $2, $3)
		ON CONFLICT (plan_id, user_id) DO NOTHING`, planID, userID, message,
	); err != nil {
		return nil, err
	}
	var jr JoinRequest
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, plan_id::text, user_id::text, status::text, message, created_at
		FROM plan_join_requests WHERE plan_id = $1 AND user_id = $2`, planID, userID,
	).Scan(&jr.ID, &jr.PlanID, &jr.UserID, &jr.Status, &jr.Message, &jr.CreatedAt)
	return &jr, err
}

func (r *Repository) GetJoinRequest(ctx context.Context, id string) (*JoinRequest, error) {
	var jr JoinRequest
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, plan_id::text, user_id::text, status::text, message, created_at
		FROM plan_join_requests WHERE id = $1`, id,
	).Scan(&jr.ID, &jr.PlanID, &jr.UserID, &jr.Status, &jr.Message, &jr.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	return &jr, err
}

// DecideJoinRequest only transitions a pending request; ok=false means it
// was already decided (the caller compares the existing status).
func (r *Repository) DecideJoinRequest(ctx context.Context, id, decidedBy string, approve bool) (jr *JoinRequest, ok bool, err error) {
	newStatus := "rejected"
	if approve {
		newStatus = "approved"
	}
	var out JoinRequest
	err = r.pool.QueryRow(ctx, `
		UPDATE plan_join_requests SET status = $2::join_request_status, decided_by = $3, decided_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING id::text, plan_id::text, user_id::text, status::text, message, created_at`,
		id, newStatus, decidedBy,
	).Scan(&out.ID, &out.PlanID, &out.UserID, &out.Status, &out.Message, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &out, true, nil
}

func (r *Repository) ListJoinRequests(ctx context.Context, planID, status string, limit int) ([]*JoinRequest, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, plan_id::text, user_id::text, status::text, message, created_at
		FROM plan_join_requests
		WHERE plan_id = $1 AND ($2 = '' OR status::text = $2)
		ORDER BY created_at DESC LIMIT $3`, planID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*JoinRequest
	for rows.Next() {
		var jr JoinRequest
		if err := rows.Scan(&jr.ID, &jr.PlanID, &jr.UserID, &jr.Status, &jr.Message, &jr.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &jr)
	}
	return out, rows.Err()
}

// InviteUsers inserts invites for existing users only and returns the ids
// that were newly invited (already-invited users are skipped, idempotent).
func (r *Repository) InviteUsers(ctx context.Context, planID, invitedBy string, userIDs []string) ([]string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		INSERT INTO plan_invites (plan_id, user_id, invited_by)
		SELECT $1, u.id, $2 FROM users u WHERE u.id = ANY($3::uuid[])
		ON CONFLICT DO NOTHING
		RETURNING user_id::text`, planID, invitedBy, userIDs)
	if err != nil {
		return nil, err
	}
	var invited []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		invited = append(invited, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range invited {
		if err := eventbus.EnqueueNotifyUser(ctx, tx, eventbus.NotifyUserPayload{
			UserID: id, Title: "You're invited", Body: "A host invited you to a plan.",
			DeepLink: "hivemind://plans/" + planID, Channel: "push",
		}); err != nil {
			return nil, err
		}
	}
	return invited, tx.Commit(ctx)
}

func (r *Repository) RevokeInvite(ctx context.Context, planID, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM plan_invites WHERE plan_id = $1 AND user_id = $2`, planID, userID)
	return err
}

// NotifyHostOfRequest / NotifyRequester enqueue NOTIFY_USER in their own
// small transaction (a lost notification must never fail the request).
func (r *Repository) Notify(ctx context.Context, p eventbus.NotifyUserPayload) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := eventbus.EnqueueNotifyUser(ctx, tx, p); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type UpcomingFilter struct {
	From, To   time.Time
	CategoryID string
	CityID     string // empty → the caller's city
	FreeOnly   bool
	Limit      int
}

// Upcoming lists discoverable plans starting in [From, To), soonest first. It
// filters through plans_discoverable (published + public, one row per
// recurring series) but reads the full row from plans, since the view's column
// list predates later columns.
func (r *Repository) Upcoming(ctx context.Context, callerID string, f UpcomingFilter) ([]*Plan, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+planColumns+` FROM plans
		WHERE id IN (
			SELECT d.id FROM plans_discoverable d
			WHERE d.starts_at >= $2::timestamptz AND d.starts_at < $3::timestamptz
			  AND (d.city_id = COALESCE(NULLIF($4,'')::uuid, (SELECT city_id FROM users WHERE id = $1::uuid))
			       OR COALESCE(NULLIF($4,'')::uuid, (SELECT city_id FROM users WHERE id = $1::uuid)) IS NULL)
			  AND (NULLIF($5,'') IS NULL OR d.category_id = NULLIF($5,'')::uuid)
			  AND (NOT $6 OR d.price_minor = 0))
		ORDER BY starts_at LIMIT $7`, callerID, f.From, f.To, f.CityID, f.CategoryID, f.FreeOnly, f.Limit)
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrInvalidInput
		}
		return nil, err
	}
	defer rows.Close()
	var out []*Plan
	for rows.Next() {
		var p Plan
		if err := scanPlan(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		if isBadUUID(err) {
			return nil, ErrInvalidInput
		}
		return nil, err
	}
	return out, nil
}

func isBadUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

// Save hearts a plan (idempotent). A malformed id is "not found".
func (r *Repository) Save(ctx context.Context, userID, planID string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO plan_saves (user_id, plan_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, planID)
	if isBadUUID(err) {
		return ErrPlanNotFound
	}
	return err
}

func (r *Repository) Unsave(ctx context.Context, userID, planID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM plan_saves WHERE user_id = $1 AND plan_id = $2`, userID, planID)
	if isBadUUID(err) {
		return nil
	}
	return err
}

// ListSaved returns the caller's saved plans, newest save first — only ones
// still visible to them (public, or their own).
func (r *Repository) ListSaved(ctx context.Context, userID string, limit int) ([]*Plan, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+planColumns+` FROM plans WHERE id IN (
			SELECT s.plan_id FROM plan_saves s JOIN plans p ON p.id = s.plan_id
			WHERE s.user_id = $1 AND p.status <> 'draft' AND (p.visibility = 'public' OR p.host_id = $1)
			ORDER BY s.created_at DESC LIMIT $2)`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Plan
	for rows.Next() {
		var p Plan
		if err := scanPlan(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// Decorate fills the per-viewer / aggregate fields of a batch of plans with two
// queries (host ratings, viewer's saves) — never one query per plan.
func (r *Repository) Decorate(ctx context.Context, plans []*Plan, viewerID string) error {
	if len(plans) == 0 {
		return nil
	}
	hostSet, ids := map[string]struct{}{}, make([]string, 0, len(plans))
	for _, p := range plans {
		hostSet[p.HostID] = struct{}{}
		ids = append(ids, p.ID)
	}
	hosts := make([]string, 0, len(hostSet))
	for h := range hostSet {
		hosts = append(hosts, h)
	}
	type rating struct {
		avg   float64
		count int32
	}
	ratings := map[string]rating{}
	rows, err := r.pool.Query(ctx, `
		SELECT p.host_id::text, AVG(rv.rating)::float8, COUNT(rv.id)::int
		FROM plans p JOIN reviews rv ON rv.plan_id = p.id
		WHERE p.host_id = ANY($1::uuid[]) GROUP BY p.host_id`, hosts)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h string
		var rt rating
		if err := rows.Scan(&h, &rt.avg, &rt.count); err != nil {
			rows.Close()
			return err
		}
		ratings[h] = rt
	}
	rows.Close()
	saved := map[string]bool{}
	if viewerID != "" {
		srows, err := r.pool.Query(ctx, `SELECT plan_id::text FROM plan_saves WHERE user_id = $1 AND plan_id = ANY($2::uuid[])`, viewerID, ids)
		if err != nil && !isBadUUID(err) {
			return err
		}
		if err == nil {
			for srows.Next() {
				var id string
				if err := srows.Scan(&id); err != nil {
					srows.Close()
					return err
				}
				saved[id] = true
			}
			srows.Close()
		}
	}
	for _, p := range plans {
		rt := ratings[p.HostID]
		p.HostRatingAvg, p.HostRatingCount, p.SavedByMe = rt.avg, rt.count, saved[p.ID]
	}
	return nil
}
