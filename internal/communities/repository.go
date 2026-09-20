// Package communities implements PRD §13.8 Communities and flow.md §20-22:
// public / private / approval / paid communities with join requests and
// invites — every RPC is fully implemented.
package communities

import (
	"context"
	"errors"
	"github.com/hivemind/backend/internal/notifications"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/eventbus"
)

var (
	ErrCommunityNotFound = errors.New("communities: community not found")
	ErrRequestNotFound   = errors.New("communities: join request not found")
)

type Community struct {
	ID                  string
	Name                string
	Description         string
	CityID              string
	OwnerID             string
	MembershipType      string // public | private | approval | paid
	CoverImageURL       string
	Rules               string
	CategoryID          string
	RequiredEntitlement string
	MemberCount         int32
	PlanCount           int32
	IsMember            bool // set by MarkMembership for a known viewer
	JoinPending         bool // the viewer has an unanswered request (approval communities)
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const communityColumns = `c.id::text, c.name, c.description, COALESCE(c.city_id::text,''), c.owner_id::text,
	c.membership_type::text, c.cover_image_url, c.rules, COALESCE(c.category_id::text,''),
	COALESCE(c.required_entitlement,''),
	(SELECT count(*) FROM community_members m WHERE m.community_id = c.id)::int,
	(SELECT count(*) FROM community_events e JOIN plans p ON p.id = e.plan_id
	   WHERE e.community_id = c.id AND p.status = 'published')::int`

func scanCommunity(row interface{ Scan(...any) error }, c *Community) error {
	return row.Scan(&c.ID, &c.Name, &c.Description, &c.CityID, &c.OwnerID, &c.MembershipType,
		&c.CoverImageURL, &c.Rules, &c.CategoryID, &c.RequiredEntitlement, &c.MemberCount, &c.PlanCount)
}

// Create inserts the community and adds the owner as its first member (role
// 'owner') in one transaction.
func (r *Repository) Create(ctx context.Context, c *Community) (*Community, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO communities (name, description, city_id, owner_id, membership_type,
			cover_image_url, rules, category_id, required_entitlement)
		VALUES ($1, $2, NULLIF($3,'')::uuid, $4::uuid, $5::community_type, $6, $7,
			NULLIF($8,'')::uuid, NULLIF($9,''))
		RETURNING id::text`,
		c.Name, c.Description, c.CityID, c.OwnerID, c.MembershipType,
		c.CoverImageURL, c.Rules, c.CategoryID, c.RequiredEntitlement,
	).Scan(&id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO community_members (community_id, user_id, role) VALUES ($1, $2, 'owner')`,
		id, c.OwnerID,
	); err != nil {
		return nil, err
	}

	var out Community
	if err := scanCommunity(tx.QueryRow(ctx, `SELECT `+communityColumns+` FROM communities c WHERE c.id = $1`, id), &out); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Community, error) {
	var c Community
	err := scanCommunity(r.pool.QueryRow(ctx, `SELECT `+communityColumns+` FROM communities c WHERE c.id = $1`, id), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCommunityNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) MemberRole(ctx context.Context, communityID, userID string) (string, error) {
	var role string
	err := r.pool.QueryRow(ctx,
		`SELECT role FROM community_members WHERE community_id = $1 AND user_id = $2`, communityID, userID,
	).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (r *Repository) HasApprovedInvite(ctx context.Context, communityID, userID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM community_join_requests
		WHERE community_id = $1 AND user_id = $2 AND status = 'approved' AND invited_by IS NOT NULL)`,
		communityID, userID,
	).Scan(&ok)
	return ok, err
}

func (r *Repository) requestStatus(ctx context.Context, communityID, userID string) (string, error) {
	var s string
	err := r.pool.QueryRow(ctx,
		`SELECT status::text FROM community_join_requests WHERE community_id = $1 AND user_id = $2`,
		communityID, userID,
	).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return s, err
}

type Membership struct {
	CommunityID string
	UserID      string
	Role        string
	Status      string // active | pending | rejected
}

// Join is idempotent — ON CONFLICT DO NOTHING, same pattern as
// chat.Repository.AddMember — a repeated join isn't an error.
func (r *Repository) Join(ctx context.Context, communityID, userID string) (*Membership, error) {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO community_members (community_id, user_id, role) VALUES ($1, $2, 'member')
		ON CONFLICT (community_id, user_id) DO NOTHING`,
		communityID, userID,
	); err != nil {
		return nil, err
	}
	role, err := r.MemberRole(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	return &Membership{CommunityID: communityID, UserID: userID, Role: role, Status: "active"}, nil
}

// RequestToJoin records a pending request for an approval community (a
// repeat request keeps its existing status, including 'rejected').
func (r *Repository) RequestToJoin(ctx context.Context, communityID, userID string) (status string, created bool, err error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO community_join_requests (community_id, user_id) VALUES ($1, $2)
		ON CONFLICT (community_id, user_id) DO NOTHING`, communityID, userID,
	)
	if err != nil {
		return "", false, err
	}
	status, err = r.requestStatus(ctx, communityID, userID)
	return status, tag.RowsAffected() == 1, err
}

type JoinRequest struct {
	ID          string
	CommunityID string
	UserID      string
	Status      string
	CreatedAt   time.Time
}

func (r *Repository) GetRequest(ctx context.Context, id string) (*JoinRequest, error) {
	var jr JoinRequest
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, community_id::text, user_id::text, status::text, created_at
		FROM community_join_requests WHERE id = $1`, id,
	).Scan(&jr.ID, &jr.CommunityID, &jr.UserID, &jr.Status, &jr.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	return &jr, err
}

func (r *Repository) ListRequests(ctx context.Context, communityID, status string, limit int) ([]*JoinRequest, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, community_id::text, user_id::text, status::text, created_at
		FROM community_join_requests
		WHERE community_id = $1 AND invited_by IS NULL AND ($2 = '' OR status::text = $2)
		ORDER BY created_at DESC LIMIT $3`, communityID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*JoinRequest
	for rows.Next() {
		var jr JoinRequest
		if err := rows.Scan(&jr.ID, &jr.CommunityID, &jr.UserID, &jr.Status, &jr.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &jr)
	}
	return out, rows.Err()
}

// DecideRequest transitions a pending request; approving inserts the member
// in the same transaction. ok=false means it was no longer pending.
func (r *Repository) DecideRequest(ctx context.Context, id, decidedBy string, approve bool) (jr *JoinRequest, ok bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	newStatus := "rejected"
	if approve {
		newStatus = "approved"
	}
	var out JoinRequest
	err = tx.QueryRow(ctx, `
		UPDATE community_join_requests SET status = $2::join_request_status, decided_by = $3, decided_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING id::text, community_id::text, user_id::text, status::text, created_at`,
		id, newStatus, decidedBy,
	).Scan(&out.ID, &out.CommunityID, &out.UserID, &out.Status, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if approve {
		if _, err := tx.Exec(ctx, `
			INSERT INTO community_members (community_id, user_id, role) VALUES ($1, $2, 'member')
			ON CONFLICT (community_id, user_id) DO NOTHING`, out.CommunityID, out.UserID,
		); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	typ, title := notifications.TypeCommunityDeclined, "{actor} declined your request to join a community"
	if approve {
		typ, title = notifications.TypeCommunityApproved, "Your request to join the community was approved"
	}
	_, _ = notifications.Emit(ctx, r.pool, notifications.Event{
		UserID: out.UserID, ActorID: decidedBy, Type: typ, TargetID: out.CommunityID, Title: title,
		DeepLink: "hivemind://communities/" + out.CommunityID, DedupeKey: "commdecide:" + out.ID,
	})
	return &out, true, nil
}

// Invite records an approved, invited_by-stamped request — what a private
// community's JoinCommunity looks for.
func (r *Repository) Invite(ctx context.Context, communityID, userID, invitedBy string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO community_join_requests (community_id, user_id, status, invited_by, decided_by, decided_at)
		SELECT $1, u.id, 'approved', $3, $3, now() FROM users u WHERE u.id = $2
		ON CONFLICT (community_id, user_id) DO UPDATE
			SET status = 'approved', invited_by = EXCLUDED.invited_by, decided_by = EXCLUDED.decided_by, decided_at = now()`,
		communityID, userID, invitedBy,
	); err != nil {
		return err
	}
	if err := eventbus.EnqueueNotifyUser(ctx, tx, eventbus.NotifyUserPayload{
		UserID: userID, Title: "Community invite", Body: "You've been invited to join a community.",
		DeepLink: "hivemind://communities/" + communityID, Channel: "push",
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) Leave(ctx context.Context, communityID, userID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM community_members WHERE community_id = $1 AND user_id = $2 AND role <> 'owner'`, communityID, userID)
	return err
}

// List excludes private communities (they never appear in discovery).
func (r *Repository) List(ctx context.Context, cityID, categoryID, query, viewerID string, onlyMine bool, limit int) ([]*Community, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+communityColumns+`
		FROM communities c
		WHERE (c.membership_type <> 'private' OR ($6 AND EXISTS (SELECT 1 FROM community_members mm WHERE mm.community_id = c.id AND mm.user_id = NULLIF($5,'')::uuid)))
		  AND (NOT $6 OR EXISTS (SELECT 1 FROM community_members mm WHERE mm.community_id = c.id AND mm.user_id = NULLIF($5,'')::uuid))
		  AND (c.city_id = NULLIF($1,'')::uuid OR $1 = '')
		  AND (c.category_id = NULLIF($2,'')::uuid OR $2 = '')
		  AND ($3 = '' OR c.name ILIKE '%' || $3 || '%')
		ORDER BY (SELECT count(*) FROM community_members m WHERE m.community_id = c.id) DESC, c.name
		LIMIT $4`, cityID, categoryID, query, limit, viewerID, onlyMine)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Community
	for rows.Next() {
		var c Community
		if err := scanCommunity(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// ListPlanIDs lists a community's published plans. Community-visibility
// plans are shown only to members; private plans never appear here.
func (r *Repository) ListPlanIDs(ctx context.Context, communityID string, isMember bool, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id FROM community_events ce
		JOIN plans p ON p.id = ce.plan_id
		WHERE ce.community_id = $1 AND p.status = 'published'
		  AND (p.visibility = 'public' OR (p.visibility = 'community' AND $2))
		ORDER BY p.starts_at
		LIMIT $3`,
		communityID, isMember, limit,
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

// MarkMembership sets IsMember on each community for viewerID with one query.
func (r *Repository) MarkMembership(ctx context.Context, list []*Community, viewerID string) error {
	if viewerID == "" || len(list) == 0 {
		return nil
	}
	ids := make([]string, len(list))
	for i, c := range list {
		ids[i] = c.ID
	}
	rows, err := r.pool.Query(ctx, `SELECT community_id::text FROM community_members WHERE user_id = $1 AND community_id = ANY($2::uuid[])`, viewerID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	mine := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		mine[id] = true
	}
	for _, c := range list {
		c.IsMember = mine[c.ID]
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// unanswered requests of the viewer (their own, not invitations)
	prows, err := r.pool.Query(ctx, `SELECT community_id::text FROM community_join_requests WHERE user_id = $1 AND status = 'pending' AND invited_by IS NULL AND community_id = ANY($2::uuid[])`, viewerID, ids)
	if err != nil {
		return err
	}
	defer prows.Close()
	pending := map[string]bool{}
	for prows.Next() {
		var id string
		if err := prows.Scan(&id); err != nil {
			return err
		}
		pending[id] = true
	}
	for _, c := range list {
		c.JoinPending = pending[c.ID] && !c.IsMember
	}
	return prows.Err()
}
