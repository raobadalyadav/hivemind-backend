// Package social implements PRD §13.10 Social Content / §9 Memories (Phase
// 2). CreatePost/GetPost are the fully working vertical slice; ListPosts/
// CommentOnPost/LikePost are typed stubs.
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
