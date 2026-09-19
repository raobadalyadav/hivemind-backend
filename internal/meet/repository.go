// Package meet is the swipe deck for making new friends: a deck of profiles,
// waves (one-way hellos), matches (mutual waves → connection + DM) and
// profile boosts.
package meet

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/eventbus"
)

var (
	ErrInvalidInput        = errors.New("meet: invalid input")
	ErrTargetNotFound      = errors.New("meet: person not found")
	ErrRateLimited         = errors.New("meet: you've reached today's limit — try again tomorrow")
	ErrAlreadyBoosted      = errors.New("meet: your profile is already boosted")
	ErrInsufficientCredits = errors.New("meet: not enough credits for a boost")
)

const (
	dailySwipes = 300
	dailyWaves  = 50
	dailySupers = 5

	BoostDuration  = 30 * time.Minute
	BoostFreeEvery = 7 * 24 * time.Hour
	BoostCostMinor = 1900 // ₹19
	Currency       = "INR"
	passRetention  = 30 * 24 * time.Hour // a pass hides someone this long, then they may reappear
)

type Photo struct {
	URL, ThumbURL string
	Width, Height int32
}

type Card struct {
	UserID, DisplayName, CityName, Bio, Occupation, Education string
	Age                                                       int32
	Interests, Hobbies, Intents, Shared                       []string
	Photos                                                    []Photo
	Verified, WavedAtYou, Boosted                             bool
	CommonCommunities                                         int32
}

type Repository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, now: time.Now}
}

// blockedEitherWay is the one definition of "these two can't see each other".
const blockedEitherWay = `EXISTS (SELECT 1 FROM blocks b WHERE (b.user_id = %[1]s AND b.blocked_user_id = %[2]s) OR (b.user_id = %[2]s AND b.blocked_user_id = %[1]s))`

// Deck returns people the caller hasn't decided on yet. Eligibility: onboarded
// (5+ interests), active, same city, not blocked either way, not already a
// connection (or pending), not swiped (a pass expires after 30 days). Order:
// boosted first, then people who already waved at the caller, then people with
// photos, then shared interests/intents, with a per-day hash as tiebreak so
// the deck is stable within a day but not identical every day.
func (r *Repository) Deck(ctx context.Context, userID string, limit, minAge, maxAge int) ([]*Card, error) {
	now := r.now()
	rows, err := r.pool.Query(ctx, `
		WITH me AS (
			SELECT u.city_id, COALESCE(up.interests, '{}') AS interests, COALESCE(pr.intents, '{}') AS intents
			FROM users u
			LEFT JOIN user_profiles up ON up.user_id = u.id
			LEFT JOIN user_preferences pr ON pr.user_id = u.id
			WHERE u.id = $1
		), cand AS (
			SELECT u.id, up.display_name, COALESCE(date_part('year', age(u.date_of_birth))::int, 0) AS age,
				COALESCE(c.name, '') AS city, up.bio, up.occupation, up.education, up.interests, up.hobbies,
				COALESCE(pr.intents, '{}') AS intents, up.selfie_verified_at IS NOT NULL AS verified,
				ARRAY(SELECT unnest(up.interests) INTERSECT SELECT unnest(me.interests)) AS shared,
				cardinality(ARRAY(SELECT unnest(COALESCE(pr.intents, '{}')) INTERSECT SELECT unnest(me.intents))) AS shared_intents,
				EXISTS (SELECT 1 FROM swipes s WHERE s.actor_id = u.id AND s.target_id = $1 AND s.action IN ('wave','super')) AS waved,
				EXISTS (SELECT 1 FROM profile_boosts b WHERE b.user_id = u.id AND $5::timestamptz >= b.starts_at AND $5::timestamptz < b.ends_at) AS boosted,
				EXISTS (SELECT 1 FROM profile_photos ph WHERE ph.user_id = u.id) AS has_photo,
				(SELECT count(*) FROM community_members a JOIN community_members b ON b.community_id = a.community_id
				  WHERE a.user_id = $1::uuid AND b.user_id = u.id)::int AS common_communities
			FROM users u
			JOIN user_profiles up ON up.user_id = u.id
			LEFT JOIN user_preferences pr ON pr.user_id = u.id
			LEFT JOIN cities c ON c.id = u.city_id, me
			WHERE u.id <> $1::uuid AND u.status = 'active' AND cardinality(up.interests) >= 5
			  AND (me.city_id IS NULL OR u.city_id = me.city_id)
			  AND NOT `+sprintfBlocked("$1::uuid", "u.id")+`
			  AND NOT EXISTS (SELECT 1 FROM connections k
					WHERE (k.requester_id = $1::uuid AND k.recipient_id = u.id) OR (k.requester_id = u.id AND k.recipient_id = $1::uuid))
			  AND NOT EXISTS (SELECT 1 FROM swipes s WHERE s.actor_id = $1::uuid AND s.target_id = u.id
					AND (s.action <> 'pass' OR s.created_at > $5::timestamptz - $6::float8 * interval '1 second'))
			  AND ($3 = 0 OR u.date_of_birth IS NULL OR date_part('year', age(u.date_of_birth)) >= $3)
			  AND ($4 = 0 OR u.date_of_birth IS NULL OR date_part('year', age(u.date_of_birth)) <= $4)
		)
		SELECT id::text, display_name, age, city, bio, occupation, education, interests, hobbies, intents,
			verified, shared, waved, boosted, common_communities
		FROM cand
		ORDER BY boosted DESC, waved DESC, has_photo DESC,
			(cardinality(shared) * 2 + shared_intents) DESC,
			md5(id::text || $1::text || to_char($5::timestamptz, 'YYYY-MM-DD'))
		LIMIT $2`,
		userID, limit, minAge, maxAge, now, passRetention.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cards []*Card
	ids := []string{}
	for rows.Next() {
		var c Card
		if err := rows.Scan(&c.UserID, &c.DisplayName, &c.Age, &c.CityName, &c.Bio, &c.Occupation, &c.Education,
			&c.Interests, &c.Hobbies, &c.Intents, &c.Verified, &c.Shared, &c.WavedAtYou, &c.Boosted, &c.CommonCommunities); err != nil {
			return nil, err
		}
		cards = append(cards, &c)
		ids = append(ids, c.UserID)
	}
	if err := rows.Err(); err != nil || len(cards) == 0 {
		return cards, err
	}
	pr, err := r.pool.Query(ctx, `
		SELECT user_id::text, url, thumb_url, width, height FROM profile_photos
		WHERE user_id = ANY($1::uuid[]) ORDER BY user_id, position, created_at`, ids)
	if err != nil {
		return nil, err
	}
	defer pr.Close()
	byUser := map[string][]Photo{}
	for pr.Next() {
		var uid string
		var p Photo
		if err := pr.Scan(&uid, &p.URL, &p.ThumbURL, &p.Width, &p.Height); err != nil {
			return nil, err
		}
		byUser[uid] = append(byUser[uid], p)
	}
	for _, c := range cards {
		c.Photos = byUser[c.UserID]
	}
	return cards, pr.Err()
}

type SwipeResult struct {
	Matched    bool
	TargetName string
}

// Swipe records the caller's decision. Waves notify the target; a wave at
// someone who already waved is a match: the two become accepted connections
// (serialised with connections.Create via the same pair advisory lock) and both
// are notified. Passing is silent. Re-swiping updates the decision.
func (r *Repository) Swipe(ctx context.Context, actor, target, action string) (*SwipeResult, error) {
	now := r.now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	lo, hi := actor, target
	if lo > hi {
		lo, hi = hi, lo
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "conn:"+lo+":"+hi); err != nil {
		return nil, err
	}

	var name string
	var ok bool
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(up.display_name, ''), u.status = 'active' AND NOT `+sprintfBlocked("$1::uuid", "u.id")+`
		FROM users u LEFT JOIN user_profiles up ON up.user_id = u.id WHERE u.id = $2::uuid`, actor, target).Scan(&name, &ok)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ok) {
		return nil, ErrTargetNotFound
	}
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrTargetNotFound
		}
		return nil, err
	}

	var swipes, waves, supers int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE action <> 'pass'), count(*) FILTER (WHERE action = 'super')
		FROM swipes WHERE actor_id = $1 AND created_at > $2::timestamptz - interval '24 hours'`, actor, now).Scan(&swipes, &waves, &supers); err != nil {
		return nil, err
	}
	if swipes >= dailySwipes || (action != "pass" && waves >= dailyWaves) || (action == "super" && supers >= dailySupers) {
		return nil, ErrRateLimited
	}

	var prev string // "" when there was no earlier decision
	if err := tx.QueryRow(ctx, `SELECT action FROM swipes WHERE actor_id = $1 AND target_id = $2`, actor, target).Scan(&prev); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	unchanged := prev == action // repeating a swipe must not notify again
	if _, err := tx.Exec(ctx, `
		INSERT INTO swipes (actor_id, target_id, action, created_at) VALUES ($1, $2, $3, $4::timestamptz)
		ON CONFLICT (actor_id, target_id) DO UPDATE SET action = EXCLUDED.action, created_at = EXCLUDED.created_at`,
		actor, target, action, now); err != nil {
		return nil, err
	}
	res := &SwipeResult{TargetName: name}
	if action == "pass" {
		return res, tx.Commit(ctx)
	}

	var mutual bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM swipes WHERE actor_id = $1 AND target_id = $2 AND action IN ('wave','super'))`,
		target, actor).Scan(&mutual); err != nil {
		return nil, err
	}
	var actorName string
	_ = tx.QueryRow(ctx, `SELECT COALESCE(display_name, '') FROM user_profiles WHERE user_id = $1`, actor).Scan(&actorName)

	if !mutual {
		if unchanged {
			return res, tx.Commit(ctx)
		}
		title, body := actorName+" waved at you 👋", "Wave back to start chatting."
		if action == "super" {
			title, body = actorName+" sent you a super wave ⭐", "They really want to meet you — wave back!"
		}
		if err := eventbus.EnqueueNotifyUser(ctx, tx, eventbus.NotifyUserPayload{
			UserID: target, Title: title, Body: body, DeepLink: "hivemind://waves", Channel: "push"}); err != nil {
			return nil, err
		}
		return res, tx.Commit(ctx)
	}

	// Match: make them connections (whichever direction a row already exists).
	tag, err := tx.Exec(ctx, `
		UPDATE connections SET status = 'accepted'::connection_status, updated_at = now()
		WHERE (requester_id = $1 AND recipient_id = $2) OR (requester_id = $2 AND recipient_id = $1)`, actor, target)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1, $2, 'accepted')`, actor, target); err != nil {
			return nil, err
		}
	}
	res.Matched = true
	if unchanged {
		return res, tx.Commit(ctx)
	}
	for _, p := range []struct{ to, other string }{{target, actorName}, {actor, name}} {
		if err := eventbus.EnqueueNotifyUser(ctx, tx, eventbus.NotifyUserPayload{
			UserID: p.to, Title: "It's a match! 🎉", Body: "You and " + p.other + " waved at each other — say hi.",
			DeepLink: "hivemind://chats", Channel: "push"}); err != nil {
			return nil, err
		}
	}
	return res, tx.Commit(ctx)
}

type Wave struct {
	UserID, DisplayName, PhotoURL, RoomID string
	Age                                   int32
	Verified, Super, Matched              bool
	CreatedAt                             time.Time
}

// Waves lists waves the caller received (incoming) or sent. Incoming hides
// people the caller passed on and anyone blocked; unmatched first, super first.
func (r *Repository) Waves(ctx context.Context, userID string, sent bool) ([]Wave, error) {
	who, other := "s.target_id", "s.actor_id" // incoming: the other person is the actor
	if sent {
		who, other = "s.actor_id", "s.target_id"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT o.id::text, COALESCE(up.display_name, ''), COALESCE(date_part('year', age(o.date_of_birth))::int, 0),
			COALESCE((SELECT ph.thumb_url FROM profile_photos ph WHERE ph.user_id = o.id ORDER BY position, created_at LIMIT 1), ''),
			up.selfie_verified_at IS NOT NULL, s.action = 'super',
			EXISTS (SELECT 1 FROM swipes r WHERE r.actor_id = s.target_id AND r.target_id = s.actor_id AND r.action IN ('wave','super')) AS matched,
			COALESCE((SELECT id::text FROM chat_rooms WHERE dm_key = least($1::text, o.id::text) || ':' || greatest($1::text, o.id::text)), ''),
			s.created_at
		FROM swipes s
		JOIN users o ON o.id = `+other+`
		LEFT JOIN user_profiles up ON up.user_id = o.id
		WHERE `+who+` = $1::uuid AND s.action IN ('wave','super') AND o.status = 'active'
		  AND NOT `+sprintfBlocked("$1::uuid", "o.id")+`
		  AND ($2 OR NOT EXISTS (SELECT 1 FROM swipes p WHERE p.actor_id = $1::uuid AND p.target_id = o.id AND p.action = 'pass'))
		ORDER BY matched, (s.action = 'super') DESC, s.created_at DESC LIMIT 100`, userID, sent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Wave
	for rows.Next() {
		var w Wave
		if err := rows.Scan(&w.UserID, &w.DisplayName, &w.Age, &w.PhotoURL, &w.Verified, &w.Super, &w.Matched, &w.RoomID, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

type Boost struct {
	Active        bool
	EndsAt        time.Time
	FreeAvailable bool
	FreeAgainAt   time.Time
	BalanceMinor  int64
}

func (r *Repository) BoostStatus(ctx context.Context, userID string) (*Boost, error) {
	return boostStatus(ctx, r.pool, userID, r.now())
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func boostStatus(ctx context.Context, q queryer, userID string, now time.Time) (*Boost, error) {
	var b Boost
	var ends, lastFree *time.Time
	if err := q.QueryRow(ctx, `
		SELECT (SELECT max(ends_at) FROM profile_boosts WHERE user_id = $1 AND ends_at > $2::timestamptz),
		       (SELECT max(starts_at) FROM profile_boosts WHERE user_id = $1 AND cost_minor = 0),
		       COALESCE((SELECT sum(amount_minor) FROM credit_ledger WHERE user_id = $1), 0)`,
		userID, now).Scan(&ends, &lastFree, &b.BalanceMinor); err != nil {
		return nil, err
	}
	if ends != nil {
		b.Active, b.EndsAt = true, *ends
	}
	b.FreeAvailable = lastFree == nil || now.Sub(*lastFree) >= BoostFreeEvery
	if lastFree != nil {
		b.FreeAgainAt = lastFree.Add(BoostFreeEvery)
	}
	return &b, nil
}

// ActivateBoost starts a 30-minute boost: free if none was free in the last 7
// days, otherwise paid from wallet credits (an append-only negative ledger
// row). Serialised per user so a double tap can't charge twice.
func (r *Repository) ActivateBoost(ctx context.Context, userID string) (*Boost, error) {
	now := r.now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "boost:"+userID); err != nil {
		return nil, err
	}
	b, err := boostStatus(ctx, tx, userID, now)
	if err != nil {
		return nil, err
	}
	if b.Active {
		return nil, ErrAlreadyBoosted
	}
	cost := int64(0)
	if !b.FreeAvailable {
		if b.BalanceMinor < BoostCostMinor {
			return nil, ErrInsufficientCredits
		}
		cost = BoostCostMinor
		if _, err := tx.Exec(ctx, `INSERT INTO credit_ledger (user_id, amount_minor, reason) VALUES ($1, $2, 'boost')`, userID, -cost); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO profile_boosts (user_id, starts_at, ends_at, cost_minor) VALUES ($1, $2::timestamptz, $3::timestamptz, $4)`,
		userID, now, now.Add(BoostDuration), cost); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return boostStatus(ctx, r.pool, userID, now)
}
