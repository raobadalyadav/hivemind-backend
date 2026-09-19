// Package stories implements flow.md §39: 24-hour content shown to the
// author's connections or one community, with an optional archive.
package stories

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidInput    = errors.New("stories: invalid input")
	ErrContentRejected = errors.New("stories: caption violates content policy")
	ErrNotMember       = errors.New("stories: you must be a member of that community")
	ErrNotFound        = errors.New("stories: story not found")
	ErrForbidden       = errors.New("stories: not your story")
)

// Lifetime is fixed server-side; clients can't pick a different expiry.
const Lifetime = 24 * time.Hour

type Story struct {
	ID          string
	AuthorID    string
	AuthorName  string
	MediaURL    string
	MediaType   string
	Caption     string
	Audience    string // connections | community
	CommunityID string
	KeepArchive bool
	CreatedAt   time.Time
	ExpiresAt   time.Time
	MediaID     string
	ThumbURL    string
	Width       int32
	Height      int32
	DurationMS  int32
	Edits       string // JSON object; "{}" when unedited
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const storyCols = `s.id::text, s.author_id::text, COALESCE(up.display_name,''), s.media_url, s.media_type, s.caption,
	s.audience::text, COALESCE(s.community_id::text,''), s.keep_archive, s.created_at, s.expires_at,
	COALESCE(s.media_id::text,''), s.thumb_url, s.width, s.height, s.duration_ms, s.edits::text`

func scanStories(rows pgx.Rows) ([]*Story, error) {
	defer rows.Close()
	var out []*Story
	for rows.Next() {
		var s Story
		if err := rows.Scan(&s.ID, &s.AuthorID, &s.AuthorName, &s.MediaURL, &s.MediaType, &s.Caption,
			&s.Audience, &s.CommunityID, &s.KeepArchive, &s.CreatedAt, &s.ExpiresAt,
			&s.MediaID, &s.ThumbURL, &s.Width, &s.Height, &s.DurationMS, &s.Edits); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (r *Repository) IsCommunityMember(ctx context.Context, communityID, userID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM community_members WHERE community_id = $1 AND user_id = $2)`, communityID, userID).Scan(&ok)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return false, nil
	}
	return ok, err
}

func (r *Repository) Create(ctx context.Context, s *Story, now time.Time) (*Story, error) {
	out := *s
	err := r.pool.QueryRow(ctx, `
		INSERT INTO stories (author_id, media_url, media_type, caption, audience, community_id, keep_archive, created_at, expires_at,
			media_id, thumb_url, width, height, duration_ms, edits)
		VALUES ($1, $2, $3, $4, $5::story_audience, NULLIF($6,'')::uuid, $7, $8::timestamptz, $9::timestamptz,
			$10::uuid, $11, $12, $13, $14, $15::jsonb)
		RETURNING id::text, created_at, expires_at`,
		s.AuthorID, s.MediaURL, s.MediaType, s.Caption, s.Audience, s.CommunityID, s.KeepArchive, now, now.Add(Lifetime),
		s.MediaID, s.ThumbURL, s.Width, s.Height, s.DurationMS, s.Edits,
	).Scan(&out.ID, &out.CreatedAt, &out.ExpiresAt)
	return &out, err
}

// ListVisible returns unexpired stories viewerID may see: their own; a
// connections-audience story from an accepted connection; a community story
// from a community the viewer belongs to — never across a block.
func (r *Repository) ListVisible(ctx context.Context, viewerID string, now time.Time) ([]*Story, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+storyCols+`
		FROM stories s LEFT JOIN user_profiles up ON up.user_id = s.author_id
		WHERE s.expires_at > $2::timestamptz
		  AND (s.author_id = $1::uuid OR (
			NOT EXISTS (SELECT 1 FROM blocks b
				WHERE (b.user_id = $1::uuid AND b.blocked_user_id = s.author_id)
				   OR (b.user_id = s.author_id AND b.blocked_user_id = $1::uuid))
			AND ((s.audience = 'connections' AND EXISTS (SELECT 1 FROM connections c
					WHERE c.status = 'accepted'::connection_status
					  AND ((c.requester_id = $1::uuid AND c.recipient_id = s.author_id)
					    OR (c.requester_id = s.author_id AND c.recipient_id = $1::uuid))))
			  OR (s.audience = 'community' AND EXISTS (SELECT 1 FROM community_members cm
					WHERE cm.community_id = s.community_id AND cm.user_id = $1::uuid)))))
		ORDER BY (s.author_id = $1::uuid) DESC, s.author_id, s.created_at`, viewerID, now)
	if err != nil {
		return nil, err
	}
	return scanStories(rows)
}

// ListMine: the author's live stories, plus (includeExpired) expired ones they archived.
func (r *Repository) ListMine(ctx context.Context, userID string, includeExpired bool, now time.Time) ([]*Story, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+storyCols+`
		FROM stories s LEFT JOIN user_profiles up ON up.user_id = s.author_id
		WHERE s.author_id = $1 AND (s.expires_at > $3::timestamptz OR ($2 AND s.keep_archive))
		ORDER BY s.created_at DESC`, userID, includeExpired, now)
	if err != nil {
		return nil, err
	}
	return scanStories(rows)
}

// Delete fetches the owner first and compares in Go.
func (r *Repository) Delete(ctx context.Context, storyID, userID string) error {
	var owner string
	err := r.pool.QueryRow(ctx, `SELECT author_id::text FROM stories WHERE id = $1`, storyID).Scan(&owner)
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "22P02") {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrForbidden
	}
	_, err = r.pool.Exec(ctx, `DELETE FROM stories WHERE id = $1 AND author_id = $2`, storyID, userID)
	return err
}

// PurgeExpired removes expired stories that weren't archived. Read paths
// already filter on expires_at, so this only reclaims space; it's a single
// DELETE, safe to run from several worker replicas.
func (r *Repository) PurgeExpired(ctx context.Context, now time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM stories WHERE expires_at <= $1::timestamptz AND NOT keep_archive`, now)
	return int(tag.RowsAffected()), err
}
