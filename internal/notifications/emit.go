package notifications

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Event types emitted by the social features. The app localizes its text from
// Type + the actor's name; Title/Body are the English fallback.
const (
	TypePostLike           = "post_like"
	TypePostComment        = "post_comment"
	TypeStoryView          = "story_view"
	TypeStoryLike          = "story_like"
	TypeProfileView        = "profile_view"
	TypeConnectionRequest  = "connection_request"
	TypeConnectionAccepted = "connection_accepted"
	TypeChatMessage        = "chat_message"
	TypeCommunityRequest   = "community_join_request"
	TypeCommunityDeclined  = "community_declined"
	TypeCommunityApproved  = "community_approved"
	TypeCommunityMember    = "community_member_joined"
	TypePlanAttendee       = "plan_attendee_joined"
	TypeReviewReceived     = "review_received"
)

// Categories the user can mute in Settings.
var categoryOf = map[string]string{
	TypePostLike:           "social",
	TypePostComment:        "social",
	TypeStoryView:          "stories",
	TypeStoryLike:          "stories",
	TypeProfileView:        "profile_views",
	TypeChatMessage:        "messages",
	TypeConnectionRequest:  "connections",
	TypeConnectionAccepted: "connections",
	TypeCommunityRequest:   "social",
	TypeCommunityDeclined:  "social",
	TypeCommunityApproved:  "social",
	TypeCommunityMember:    "social",
	TypePlanAttendee:       "social",
	TypeReviewReceived:     "social",
}

// Categories lists the mutable categories, for validation.
var Categories = []string{"social", "stories", "profile_views", "messages", "connections"}

// Event is one thing that happened to UserID, caused by ActorID. "{actor}" in
// Title/Body is replaced by the actor's display name.
type Event struct {
	UserID    string
	ActorID   string // empty for system events
	Type      string
	TargetID  string
	Title     string
	Body      string
	DeepLink  string
	DedupeKey string // a repeat with the same key collapses while the earlier one is unread
}

// DB is satisfied by *pgxpool.Pool and pgx.Tx, so a domain can emit inside its own transaction.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var _ DB = (pgx.Tx)(nil)

// Emit records an in-app notification. It is the single place that decides
// whether one should exist: never to yourself, never across a block (either
// direction), never to a non-active account, never in a category the
// recipient muted, never a duplicate of an unread one. It reports whether a
// row was created. Emit is deliberately synchronous and dependency-free (no
// worker/NATS): the row is visible the moment the triggering action commits.
func Emit(ctx context.Context, db DB, e Event) (bool, error) {
	if e.UserID == "" || e.Type == "" || e.Title == "" {
		return false, ErrInvalidInput
	}
	tag, err := db.Exec(ctx, `
		WITH a AS (SELECT COALESCE((SELECT display_name FROM user_profiles WHERE user_id = NULLIF($3,'')::uuid), 'Someone') AS name)
		INSERT INTO notifications (user_id, channel, type, actor_id, target_id, title, body, deep_link, dedupe_key, read)
		SELECT $1::uuid, 'in_app', $2, NULLIF($3,'')::uuid, NULLIF($4,''),
		       replace($5, '{actor}', a.name), replace($6, '{actor}', a.name), NULLIF($7,''), NULLIF($8,''), false
		FROM a
		WHERE $1::uuid IS DISTINCT FROM NULLIF($3,'')::uuid
		  AND EXISTS (SELECT 1 FROM users u WHERE u.id = $1::uuid AND u.status = 'active')
		  AND ($3 = '' OR NOT EXISTS (SELECT 1 FROM blocks b
		         WHERE (b.user_id = $1::uuid AND b.blocked_user_id = $3::uuid)
		            OR (b.user_id = $3::uuid AND b.blocked_user_id = $1::uuid)))
		  AND NOT EXISTS (SELECT 1 FROM notification_preferences p
		         WHERE p.user_id = $1::uuid AND $9 = ANY (p.muted_categories))
		ON CONFLICT DO NOTHING`,
		e.UserID, e.Type, e.ActorID, e.TargetID, e.Title, e.Body, e.DeepLink, e.DedupeKey, categoryOf[e.Type])
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
