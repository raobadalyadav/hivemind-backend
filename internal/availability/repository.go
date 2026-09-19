// Package availability implements flow.md §27 "Who's Free" and §28
// "Activity Buddy" — one mechanism for both: a user broadcasts a
// time-boxed availability window + activity type, another user with an
// overlapping window can be found as a candidate, and mutual accept
// creates an ephemeral chat room. Every RPC is fully implemented.
package availability

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrWindowNotFound = errors.New("availability: window not found")
	ErrNotOpen        = errors.New("availability: window is no longer open")
)

type Window struct {
	ID           string
	UserID       string
	ActivityType string
	StartsAt     time.Time
	EndsAt       time.Time
	Status       string
}

type Candidate struct {
	WindowID string
	UserID   string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, w *Window) (*Window, error) {
	out := *w
	out.Status = "open"
	err := r.pool.QueryRow(ctx, `
		INSERT INTO availability_windows (user_id, activity_type, starts_at, ends_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		w.UserID, w.ActivityType, w.StartsAt, w.EndsAt,
	).Scan(&out.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Window, error) {
	var w Window
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, activity_type, starts_at, ends_at, status
		FROM availability_windows WHERE id = $1`, id,
	).Scan(&w.ID, &w.UserID, &w.ActivityType, &w.StartsAt, &w.EndsAt, &w.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWindowNotFound
		}
		return nil, err
	}
	return &w, nil
}

func (r *Repository) Cancel(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE availability_windows SET status = 'cancelled'::availability_status WHERE id = $1`, id,
	)
	return err
}

// FindMatches finds other open windows with the same activity_type, an
// overlapping time range, within 10km (users.last_location, already
// populated for proximity features), ranked by interest overlap as a
// tiebreak. activity_type is compared with case-insensitive equality
// (lower() = lower()), not ILIKE — ILIKE would let a client-supplied
// activity_type containing '%'/'_' wildcards match far more windows than
// intended, enumerating other users' availability broadly.
func (r *Repository) FindMatches(ctx context.Context, callerID string, w *Window, limit int) ([]*Candidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT aw.id, aw.user_id
		FROM availability_windows aw
		JOIN users u ON u.id = aw.user_id
		WHERE aw.status = 'open'::availability_status
		  AND aw.user_id != $1
		  AND lower(aw.activity_type) = lower($2)
		  AND aw.starts_at < $4 AND aw.ends_at > $3
		  AND ST_DWithin(u.last_location, (SELECT last_location FROM users WHERE id = $1), 10000)
		ORDER BY cardinality(ARRAY(
			SELECT unnest((SELECT interests FROM user_profiles WHERE user_id = aw.user_id))
			INTERSECT
			SELECT unnest((SELECT interests FROM user_profiles WHERE user_id = $1))
		)) DESC
		LIMIT $5`,
		callerID, w.ActivityType, w.StartsAt, w.EndsAt, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.WindowID, &c.UserID); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// AcceptMatch marks both windows 'matched' and records the room in one
// transaction, re-verifying both are still 'open' first — a window
// accepted or cancelled between FindMatches and AcceptActivityMatch must
// not be double-matched.
func (r *Repository) AcceptMatch(ctx context.Context, myWindowID, theirWindowID, roomID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var myUserID, theirUserID, myStatus, theirStatus string
	if err := tx.QueryRow(ctx,
		`SELECT user_id, status FROM availability_windows WHERE id = $1 FOR UPDATE`, myWindowID,
	).Scan(&myUserID, &myStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWindowNotFound
		}
		return err
	}
	if err := tx.QueryRow(ctx,
		`SELECT user_id, status FROM availability_windows WHERE id = $1 FOR UPDATE`, theirWindowID,
	).Scan(&theirUserID, &theirStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWindowNotFound
		}
		return err
	}
	if myStatus != "open" || theirStatus != "open" {
		return ErrNotOpen
	}

	if _, err := tx.Exec(ctx, `
		UPDATE availability_windows SET status = 'matched'::availability_status,
			matched_with_user_id = $2, chat_room_id = $3 WHERE id = $1`,
		myWindowID, theirUserID, roomID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE availability_windows SET status = 'matched'::availability_status,
			matched_with_user_id = $2, chat_room_id = $3 WHERE id = $1`,
		theirWindowID, myUserID, roomID,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
