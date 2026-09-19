// Package social implements PRD §13.10 Social Content / §9 Memories (Phase
// 2) — every RPC is fully implemented.
package social

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Media struct {
	URL  string
	Type string // image | video
}

type Post struct {
	ID           string
	AuthorID     string
	PlanID       string
	CommunityID  string
	Body         string
	MediaURLs    []string // legacy image list, kept for API compatibility
	Media        []Media
	Visibility   string // private | public | connections | community
	CreatedAt    time.Time
	LikeCount    int32
	CommentCount int32
	LikedByMe    bool
	SavedByMe    bool
}

var ErrPostNotFound = errors.New("social: post not found")

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// visibleSQL is the ONE definition of "caller ($C) may see post p": the
// author always; otherwise no block in either direction AND the post is
// public, or connections-only with an accepted connection to the author, or
// community-only with the caller in that community. Every read path
// (single get, author list, feed, saved) filters through it.
func visibleSQL(caller string) string {
	return strings.ReplaceAll(`(p.author_id = $C::uuid OR (
		NOT EXISTS (SELECT 1 FROM blocks b
			WHERE (b.user_id = $C::uuid AND b.blocked_user_id = p.author_id)
			   OR (b.user_id = p.author_id AND b.blocked_user_id = $C::uuid))
		AND (p.visibility = 'public'
		  OR (p.visibility = 'connections' AND EXISTS (SELECT 1 FROM connections c
				WHERE c.status = 'accepted'::connection_status
				  AND ((c.requester_id = $C::uuid AND c.recipient_id = p.author_id)
				    OR (c.requester_id = p.author_id AND c.recipient_id = $C::uuid))))
		  OR (p.visibility = 'community' AND EXISTS (SELECT 1 FROM community_members cm
				WHERE cm.community_id = p.community_id AND cm.user_id = $C::uuid)))))`, "$C", caller)
}

const postCols = `p.id::text, p.author_id::text, COALESCE(p.plan_id::text,''), COALESCE(p.community_id::text,''),
	p.body, p.visibility, p.created_at`

func scanPosts(rows pgx.Rows) ([]*Post, error) {
	defer rows.Close()
	var out []*Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.AuthorID, &p.PlanID, &p.CommunityID, &p.Body, &p.Visibility, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// hydrate fills media, counts and the viewer's own like/save flags for a page
// of posts with two queries (not one per post).
func (r *Repository) hydrate(ctx context.Context, posts []*Post, viewerID string) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]string, len(posts))
	byID := make(map[string]*Post, len(posts))
	for i, p := range posts {
		ids[i], byID[p.ID] = p.ID, p
	}
	rows, err := r.pool.Query(ctx,
		`SELECT post_id::text, media_url, media_type FROM post_media WHERE post_id = ANY($1::uuid[]) ORDER BY post_id, position`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var m Media
		if err := rows.Scan(&id, &m.URL, &m.Type); err != nil {
			rows.Close()
			return err
		}
		p := byID[id]
		p.Media = append(p.Media, m)
		if m.Type == "image" {
			p.MediaURLs = append(p.MediaURLs, m.URL)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	stats, err := r.pool.Query(ctx, `
		SELECT p.id::text,
			(SELECT count(*) FROM likes l WHERE l.post_id = p.id),
			(SELECT count(*) FROM comments c WHERE c.post_id = p.id),
			EXISTS(SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $2),
			EXISTS(SELECT 1 FROM post_saves s WHERE s.post_id = p.id AND s.user_id = $2)
		FROM posts p WHERE p.id = ANY($1::uuid[])`, ids, viewerID)
	if err != nil {
		return err
	}
	defer stats.Close()
	for stats.Next() {
		var id string
		var lc, cc int32
		var liked, saved bool
		if err := stats.Scan(&id, &lc, &cc, &liked, &saved); err != nil {
			return err
		}
		p := byID[id]
		p.LikeCount, p.CommentCount, p.LikedByMe, p.SavedByMe = lc, cc, liked, saved
	}
	return stats.Err()
}

func isBadUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

// Create inserts the post and its media rows in one transaction.
func (r *Repository) Create(ctx context.Context, p *Post) (*Post, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := *p
	if err := tx.QueryRow(ctx, `
		INSERT INTO posts (author_id, plan_id, community_id, body, visibility)
		VALUES ($1, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, $5)
		RETURNING id::text, created_at`,
		p.AuthorID, p.PlanID, p.CommunityID, p.Body, p.Visibility,
	).Scan(&out.ID, &out.CreatedAt); err != nil {
		return nil, err
	}
	for i, m := range p.Media {
		if _, err := tx.Exec(ctx,
			`INSERT INTO post_media (post_id, media_url, media_type, position) VALUES ($1, $2, $3, $4)`,
			out.ID, m.URL, m.Type, i,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetVisible returns the post only if viewerID may see it; a hidden post and
// a missing one are both ErrPostNotFound.
func (r *Repository) GetVisible(ctx context.Context, id, viewerID string) (*Post, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+postCols+` FROM posts p WHERE p.id = $1 AND `+visibleSQL("$2"), id, viewerID)
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrPostNotFound
		}
		return nil, err
	}
	posts, err := scanPosts(rows)
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrPostNotFound
		}
		return nil, err
	}
	if len(posts) == 0 {
		return nil, ErrPostNotFound
	}
	return posts[0], r.hydrate(ctx, posts, viewerID)
}

// ListForAuthor returns the author's posts that viewerID may see.
func (r *Repository) ListForAuthor(ctx context.Context, authorID, viewerID string, limit int) ([]*Post, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+postCols+` FROM posts p
		WHERE p.author_id = $1 AND `+visibleSQL("$2")+`
		ORDER BY p.created_at DESC, p.id DESC LIMIT $3`, authorID, viewerID, limit)
	if err != nil {
		return nil, err
	}
	posts, err := scanPosts(rows)
	if err != nil {
		return nil, err
	}
	return posts, r.hydrate(ctx, posts, viewerID)
}

// Feed pages newest-first with a (created_at, id) keyset cursor so paging is
// stable while new posts arrive. scope: global | connections | community.
func (r *Repository) Feed(ctx context.Context, viewerID, scope, communityID string, cursor *cursor, limit int) ([]*Post, error) {
	q := `SELECT ` + postCols + ` FROM posts p WHERE ` + visibleSQL("$1")
	args := []any{viewerID}
	switch scope {
	case "global":
		q += ` AND p.visibility <> 'private'`
	case "connections":
		q += ` AND p.author_id <> $1::uuid AND p.visibility IN ('public','connections') AND EXISTS (SELECT 1 FROM connections c
			WHERE c.status = 'accepted'::connection_status
			  AND ((c.requester_id = $1::uuid AND c.recipient_id = p.author_id) OR (c.requester_id = p.author_id AND c.recipient_id = $1::uuid)))`
	case "community":
		args = append(args, communityID)
		q += ` AND p.community_id = $2::uuid`
	}
	if cursor != nil {
		args = append(args, cursor.At, cursor.ID)
		q += fmt.Sprintf(` AND (p.created_at, p.id) < ($%d::timestamptz, $%d::uuid)`, len(args)-1, len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(` ORDER BY p.created_at DESC, p.id DESC LIMIT $%d`, len(args))
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrPostNotFound
		}
		return nil, err
	}
	posts, err := scanPosts(rows)
	if err != nil {
		return nil, err
	}
	return posts, r.hydrate(ctx, posts, viewerID)
}

func (r *Repository) IsCommunityMember(ctx context.Context, communityID, userID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM community_members WHERE community_id = $1 AND user_id = $2)`, communityID, userID).Scan(&ok)
	if isBadUUID(err) {
		return false, nil
	}
	return ok, err
}

// Save/Unsave are idempotent.
func (r *Repository) Save(ctx context.Context, postID, userID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO post_saves (user_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, postID)
	return err
}

func (r *Repository) Unsave(ctx context.Context, postID, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM post_saves WHERE user_id = $1 AND post_id = $2`, userID, postID)
	if isBadUUID(err) {
		return nil
	}
	return err
}

// ListSaved returns what the user saved that they can still see (a post that
// has since become hidden — block, left community — drops out).
func (r *Repository) ListSaved(ctx context.Context, userID string, limit int) ([]*Post, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+postCols+` FROM post_saves s JOIN posts p ON p.id = s.post_id
		WHERE s.user_id = $1 AND `+visibleSQL("$1")+` ORDER BY s.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	posts, err := scanPosts(rows)
	if err != nil {
		return nil, err
	}
	return posts, r.hydrate(ctx, posts, userID)
}

type Comment struct {
	ID       string
	PostID   string
	AuthorID string
	Body     string
}

func (r *Repository) CreateComment(ctx context.Context, c *Comment) (*Comment, error) {
	out := *c
	err := r.pool.QueryRow(ctx,
		`INSERT INTO comments (post_id, author_id, body) VALUES ($1, $2, $3) RETURNING id`,
		c.PostID, c.AuthorID, c.Body,
	).Scan(&out.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Like is idempotent (ON CONFLICT DO NOTHING — a repeated like isn't an
// error) and returns the current total count either way.
func (r *Repository) Like(ctx context.Context, postID, userID string) (int32, error) {
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO likes (post_id, user_id) VALUES ($1, $2) ON CONFLICT (post_id, user_id) DO NOTHING`,
		postID, userID,
	); err != nil {
		return 0, err
	}
	var count int32
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM likes WHERE post_id = $1`, postID,
	).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// CanAttachToPlan: only someone who attended a plan (or hosts it) may tag a
// post with it — otherwise anyone could spam a plan's memory feed.
func (r *Repository) CanAttachToPlan(ctx context.Context, planID, userID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM attended_bookings WHERE plan_id = $1 AND user_id = $2)
		    OR EXISTS(SELECT 1 FROM plans WHERE id = $1 AND host_id = $2)`, planID, userID).Scan(&ok)
	return ok, err
}

type Memory struct {
	PlanID      string
	Title       string
	StartsAt    time.Time
	CityName    string
	Year        int32
	PhotoCount  int32
	PeopleCount int32
}

// ListMemories returns the caller's attended plans, newest first, with the
// year taken in the plan's own city timezone. photo_count counts media on
// posts tagged with the plan that the caller may see (their own or public).
func (r *Repository) ListMemories(ctx context.Context, userID string, year int32) ([]Memory, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id::text, p.title, p.starts_at, COALESCE(c.name, ''),
			EXTRACT(year FROM p.starts_at AT TIME ZONE COALESCE(c.timezone, 'UTC'))::int,
			(SELECT count(*) FROM post_media pm JOIN posts po ON po.id = pm.post_id
			   WHERE po.plan_id = p.id AND (po.author_id = $1 OR po.visibility = 'public')),
			(SELECT count(*) FROM attended_bookings x WHERE x.plan_id = p.id)
		FROM attended_bookings ab
		JOIN plans p ON p.id = ab.plan_id
		LEFT JOIN cities c ON c.id = p.city_id
		WHERE ab.user_id = $1
		  AND ($2::int = 0 OR EXTRACT(year FROM p.starts_at AT TIME ZONE COALESCE(c.timezone, 'UTC'))::int = $2)
		ORDER BY p.starts_at DESC`, userID, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.PlanID, &m.Title, &m.StartsAt, &m.CityName, &m.Year, &m.PhotoCount, &m.PeopleCount); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
