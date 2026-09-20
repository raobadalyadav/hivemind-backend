// Package profiles implements PRD §13.2 Profile & Identity — every RPC is
// fully implemented.
package profiles

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Profile struct {
	UserID             string
	DisplayName        string
	Bio                string
	Interests          []string
	Languages          []string
	Occupation         string
	VerificationStatus string
	Gender             string
	Education          string
	Hobbies            []string
	Photos             []Photo
	SelfieVerified     bool
	ShowInPreviews     bool // privacy: appear in "who's going" cards
	HideProfileViews   bool // privacy: nobody is told I viewed them (and I'm not told about theirs)
	CreatedAt          time.Time
}

// Update is a partial update: nil pointer / nil slice = leave unchanged.
// ponytail: an empty repeated field can't be told apart from an omitted one
// over proto3, so interests/languages/hobbies can be replaced but not
// cleared; add StringList wrappers if clearing is ever needed.
type Update struct {
	UserID     string
	Bio        *string
	Occupation *string
	Gender     *string
	Education  *string
	Interests  []string
	Languages  []string
	Hobbies    []string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const profileCols = `display_name, bio, interests, languages, occupation, verification_status,
	COALESCE(gender,''), education, hobbies, selfie_verified_at IS NOT NULL, show_in_participant_previews, hide_profile_views, created_at`

func scanProfile(row interface{ Scan(...any) error }, p *Profile) error {
	return row.Scan(&p.DisplayName, &p.Bio, &p.Interests, &p.Languages, &p.Occupation,
		&p.VerificationStatus, &p.Gender, &p.Education, &p.Hobbies, &p.SelfieVerified, &p.ShowInPreviews, &p.HideProfileViews, &p.CreatedAt)
}

// Create fills in the (empty) profile row created during signup — see
// internal/auth.Repository.CreateUser.
func (r *Repository) Create(ctx context.Context, p *Profile) (*Profile, error) {
	out := Profile{UserID: p.UserID}
	err := scanProfile(r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET display_name = $2, interests = $3, updated_at = now()
		WHERE user_id = $1
		RETURNING `+profileCols,
		p.UserID, p.DisplayName, p.Interests,
	), &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, userID string) (*Profile, error) {
	p := Profile{UserID: userID}
	if err := scanProfile(r.pool.QueryRow(ctx,
		`SELECT `+profileCols+` FROM user_profiles WHERE user_id = $1`, userID), &p); err != nil {
		return nil, err
	}
	photos, err := r.ListPhotos(ctx, userID)
	if err != nil {
		return nil, err
	}
	p.Photos = photos
	return &p, nil
}

func (r *Repository) Update(ctx context.Context, u *Update) (*Profile, error) {
	p := Profile{UserID: u.UserID}
	err := scanProfile(r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET
			bio        = COALESCE($2, bio),
			occupation = COALESCE($3, occupation),
			gender     = COALESCE(NULLIF($4::text,''), gender),
			education  = COALESCE($5, education),
			interests  = COALESCE($6::text[], interests),
			languages  = COALESCE($7::text[], languages),
			hobbies    = COALESCE($8::text[], hobbies),
			updated_at = now()
		WHERE user_id = $1
		RETURNING `+profileCols,
		u.UserID, u.Bio, u.Occupation, u.Gender, u.Education, u.Interests, u.Languages, u.Hobbies,
	), &p)
	if err != nil {
		return nil, err
	}
	photos, err := r.ListPhotos(ctx, u.UserID)
	if err != nil {
		return nil, err
	}
	p.Photos = photos
	return &p, nil
}

// SetPrivacy changes only the settings that are non-nil.
func (r *Repository) SetPrivacy(ctx context.Context, userID string, showInPreviews, hideViews *bool) (*Profile, error) {
	p := Profile{UserID: userID}
	err := scanProfile(r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET
			show_in_participant_previews = COALESCE($2, show_in_participant_previews),
			hide_profile_views = COALESCE($3, hide_profile_views),
			updated_at = now()
		WHERE user_id = $1
		RETURNING `+profileCols, userID, showInPreviews, hideViews), &p)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Stats are the Me tab's counts, straight from the tables (no client-side capping).
type Stats struct {
	PlansAttended, PlansUpcoming, Connections, Communities int32
}

func (r *Repository) Stats(ctx context.Context, userID string) (*Stats, error) {
	var s Stats
	err := r.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM bookings WHERE user_id = $1 AND status = 'attended')::int,
		  (SELECT count(*) FROM bookings b JOIN plans p ON p.id = b.plan_id
		     WHERE b.user_id = $1 AND b.status = 'confirmed' AND p.starts_at > now())::int,
		  (SELECT count(*) FROM connections WHERE status = 'accepted'::connection_status AND (requester_id = $1 OR recipient_id = $1))::int,
		  (SELECT count(*) FROM community_members WHERE user_id = $1)::int`, userID).Scan(&s.PlansAttended, &s.PlansUpcoming, &s.Connections, &s.Communities)
	return &s, err
}

// UserStats is what a profile page shows about another person.
type UserStats struct {
	Connections, PlansAttended, MutualConnections, SharedCommunities, PlansHosted int32
	SharedCommunityNames                                                          []string
	MemberSince                                                                   time.Time
	HostRatingAvg                                                                 float64
	HostRatingCount                                                               int32
}

// ErrBlockedOrGone: the target doesn't exist / isn't active, or a block exists in either direction.
var ErrBlockedOrGone = errors.New("profiles: user not available")

// CanView reports whether viewer may see target's profile at all (active, not blocked either way).
func (r *Repository) CanView(ctx context.Context, viewerID, targetID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users u WHERE u.id = $2::uuid AND u.status = 'active')
		   AND NOT EXISTS (SELECT 1 FROM blocks b WHERE (b.user_id = $1::uuid AND b.blocked_user_id = $2::uuid)
		                                              OR (b.user_id = $2::uuid AND b.blocked_user_id = $1::uuid))`,
		viewerID, targetID).Scan(&ok)
	return ok, err
}

func (r *Repository) UserStats(ctx context.Context, viewerID, targetID string) (*UserStats, error) {
	ok, err := r.CanView(ctx, viewerID, targetID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrBlockedOrGone
	}
	var s UserStats
	err = r.pool.QueryRow(ctx, `
		WITH mine AS (
			SELECT CASE WHEN requester_id = $1::uuid THEN recipient_id ELSE requester_id END AS uid
			FROM connections WHERE status = 'accepted'::connection_status AND $1::uuid IN (requester_id, recipient_id)
		), theirs AS (
			SELECT CASE WHEN requester_id = $2::uuid THEN recipient_id ELSE requester_id END AS uid
			FROM connections WHERE status = 'accepted'::connection_status AND $2::uuid IN (requester_id, recipient_id)
		)
		SELECT (SELECT count(*) FROM theirs)::int,
		       (SELECT count(*) FROM bookings WHERE user_id = $2::uuid AND status = 'attended')::int,
		       (SELECT count(*) FROM theirs WHERE uid IN (SELECT uid FROM mine))::int,
		       (SELECT count(*) FROM community_members a JOIN community_members b ON b.community_id = a.community_id
		          WHERE a.user_id = $1::uuid AND b.user_id = $2::uuid)::int,
		       COALESCE((SELECT array_agg(name) FROM (SELECT c.name FROM community_members a
		          JOIN community_members b ON b.community_id = a.community_id
		          JOIN communities c ON c.id = a.community_id
		          WHERE a.user_id = $1::uuid AND b.user_id = $2::uuid ORDER BY c.name LIMIT 3) x), '{}'),
		       (SELECT created_at FROM users WHERE id = $2::uuid),
		       COALESCE((SELECT avg(r.rating)::float8 FROM reviews r JOIN plans p ON p.id = r.plan_id WHERE p.host_id = $2::uuid), 0),
		       (SELECT count(*) FROM reviews r JOIN plans p ON p.id = r.plan_id WHERE p.host_id = $2::uuid)::int,
		       (SELECT count(*) FROM plans_discoverable WHERE host_id = $2::uuid)::int`,
		viewerID, targetID).Scan(&s.Connections, &s.PlansAttended, &s.MutualConnections, &s.SharedCommunities,
		&s.SharedCommunityNames, &s.MemberSince, &s.HostRatingAvg, &s.HostRatingCount, &s.PlansHosted)
	return &s, err
}

// RecordView stores today's view and reports whether it was the first from this
// viewer to this person today. It records nothing (false) when either side hides
// views, when the viewer is the target, or when a block/inactive account applies.
func (r *Repository) RecordView(ctx context.Context, viewerID, targetID string) (bool, error) {
	if viewerID == targetID {
		return false, nil
	}
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO profile_views (viewer_id, viewed_id)
		SELECT $1::uuid, $2::uuid
		WHERE NOT EXISTS (SELECT 1 FROM user_profiles WHERE user_id IN ($1::uuid, $2::uuid) AND hide_profile_views)
		  AND EXISTS (SELECT 1 FROM users u WHERE u.id = $2::uuid AND u.status = 'active')
		  AND NOT EXISTS (SELECT 1 FROM blocks b WHERE (b.user_id = $1::uuid AND b.blocked_user_id = $2::uuid)
		                                             OR (b.user_id = $2::uuid AND b.blocked_user_id = $1::uuid))
		ON CONFLICT DO NOTHING`, viewerID, targetID)
	return tag.RowsAffected() == 1, err
}
