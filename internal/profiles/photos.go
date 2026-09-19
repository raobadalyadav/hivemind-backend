package profiles

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/hivemind/backend/pkg/media"
)

const MaxPhotos = 6

var (
	ErrTooManyPhotos = errors.New("profiles: at most 6 photos")
	ErrPhotoNotFound = errors.New("profiles: photo not found")
	ErrForbidden     = errors.New("profiles: not your photo")
)

type Photo struct {
	ID       string
	URL      string
	Position int32
	ThumbURL string
	Width    int32
	Height   int32
}

func (r *Repository) ListPhotos(ctx context.Context, userID string) ([]Photo, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, url, position, thumb_url, width, height FROM profile_photos
		WHERE user_id = $1 ORDER BY position, created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.ID, &p.URL, &p.Position, &p.ThumbURL, &p.Width, &p.Height); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddPhoto serialises per user with an advisory lock so two concurrent adds
// can't both slip past the 6-photo cap.
func (r *Repository) AddPhoto(ctx context.Context, userID string, a media.Asset) (*Photo, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "profile_photos:"+userID); err != nil {
		return nil, err
	}
	var n int
	var next int32
	if err := tx.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(position)+1, 0) FROM profile_photos WHERE user_id = $1`, userID,
	).Scan(&n, &next); err != nil {
		return nil, err
	}
	if n >= MaxPhotos {
		return nil, ErrTooManyPhotos
	}
	p := Photo{URL: a.URL, Position: next, ThumbURL: a.ThumbURL, Width: a.Width, Height: a.Height}
	if err := tx.QueryRow(ctx,
		`INSERT INTO profile_photos (user_id, url, position, media_id, thumb_url, width, height) VALUES ($1, $2, $3, $4::uuid, $5, $6, $7) RETURNING id::text`,
		userID, a.URL, next, a.ID, a.ThumbURL, a.Width, a.Height).Scan(&p.ID); err != nil {
		return nil, err
	}
	return &p, tx.Commit(ctx)
}

// DeletePhoto fetches the owner first and compares in Go (not a bare
// DELETE ... WHERE user_id) so a foreign photo id is Forbidden, not silently
// a no-op.
func (r *Repository) DeletePhoto(ctx context.Context, userID, photoID string) error {
	var owner string
	err := r.pool.QueryRow(ctx, `SELECT user_id::text FROM profile_photos WHERE id = $1`, photoID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPhotoNotFound
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrForbidden
	}
	_, err = r.pool.Exec(ctx, `DELETE FROM profile_photos WHERE id = $1 AND user_id = $2`, photoID, userID)
	return err
}

// ReorderPhotos requires photoIDs to be exactly the caller's current set.
func (r *Repository) ReorderPhotos(ctx context.Context, userID string, photoIDs []string) ([]Photo, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "profile_photos:"+userID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM profile_photos WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	var have []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		have = append(have, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	want := append([]string(nil), photoIDs...)
	sort.Strings(have)
	sort.Strings(want)
	if len(have) != len(want) {
		return nil, ErrInvalidInput
	}
	for i := range have {
		if have[i] != want[i] || (i > 0 && want[i] == want[i-1]) {
			return nil, ErrInvalidInput
		}
	}
	for i, id := range photoIDs {
		if _, err := tx.Exec(ctx, `UPDATE profile_photos SET position = $2 WHERE id = $1 AND user_id = $3`, id, i, userID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.ListPhotos(ctx, userID)
}
