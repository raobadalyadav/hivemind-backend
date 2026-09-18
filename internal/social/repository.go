// Package social implements PRD §13.10 Social Content / §9 Memories (Phase
// 2) — every RPC is fully implemented.
package social

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Post struct {
	ID         string
	AuthorID   string
	PlanID     string
	Body       string
	MediaURLs  []string
	Visibility string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts the post and its media rows in one transaction.
func (r *Repository) Create(ctx context.Context, p *Post) (*Post, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := *p
	if out.Visibility == "" {
		out.Visibility = "private"
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO posts (author_id, plan_id, body, visibility)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4)
		RETURNING id`,
		p.AuthorID, p.PlanID, p.Body, out.Visibility,
	).Scan(&out.ID); err != nil {
		return nil, err
	}

	for i, url := range p.MediaURLs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO post_media (post_id, media_url, position) VALUES ($1, $2, $3)`,
			out.ID, url, i,
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Post, error) {
	var p Post
	err := r.pool.QueryRow(ctx, `
		SELECT id, author_id, COALESCE(plan_id::text,''), body, visibility
		FROM posts WHERE id = $1`, id,
	).Scan(&p.ID, &p.AuthorID, &p.PlanID, &p.Body, &p.Visibility)
	if err != nil {
		return nil, err
	}

	rows, err := r.pool.Query(ctx,
		`SELECT media_url FROM post_media WHERE post_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, err
		}
		p.MediaURLs = append(p.MediaURLs, url)
	}
	return &p, rows.Err()
}

// ListForAuthor returns posts by author, filtered to visibility='public'
// unless the caller is the author themselves (private posts stay private —
// see service.go).
func (r *Repository) ListForAuthor(ctx context.Context, authorID string, publicOnly bool, limit int) ([]*Post, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, author_id, COALESCE(plan_id::text,''), body, visibility
		FROM posts WHERE author_id = $1 AND (NOT $2 OR visibility = 'public')
		ORDER BY created_at DESC LIMIT $3`,
		authorID, publicOnly, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []*Post
	ids := make([]string, 0)
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.AuthorID, &p.PlanID, &p.Body, &p.Visibility); err != nil {
			return nil, err
		}
		posts = append(posts, &p)
		ids = append(ids, p.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return posts, nil
	}

	mediaRows, err := r.pool.Query(ctx,
		`SELECT post_id, media_url FROM post_media WHERE post_id = ANY($1::uuid[]) ORDER BY post_id, position`, ids)
	if err != nil {
		return nil, err
	}
	defer mediaRows.Close()
	mediaByPost := make(map[string][]string)
	for mediaRows.Next() {
		var postID, url string
		if err := mediaRows.Scan(&postID, &url); err != nil {
			return nil, err
		}
		mediaByPost[postID] = append(mediaByPost[postID], url)
	}
	if err := mediaRows.Err(); err != nil {
		return nil, err
	}
	for _, p := range posts {
		p.MediaURLs = mediaByPost[p.ID]
	}
	return posts, nil
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
