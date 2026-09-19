// Package externalevents implements flow.md §29: admin-curated third-party
// events that users can mark interest in, see who else is interested, and
// start a group chat for. No ticketing or provider sync (out of scope).
package externalevents

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidInput  = errors.New("externalevents: invalid input")
	ErrNotFound      = errors.New("externalevents: event not found")
	ErrNotInterested = errors.New("externalevents: mark yourself interested first")
)

type Event struct {
	ID, CityID, CategoryID, Title, Description, Source, SourceURL, VenueName, ImageURL string
	StartsAt                                                                           time.Time
	EndsAt                                                                             *time.Time
	InterestedCount                                                                    int32
	IAmInterested, HasGroup                                                            bool
}

type Person struct {
	UserID, DisplayName string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func isBadUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

const eventCols = `e.id::text, e.city_id::text, COALESCE(e.category_id::text,''), e.title, e.description, e.source, e.source_url,
	e.venue_name, e.image_url, e.starts_at, e.ends_at,
	(SELECT count(*) FROM external_event_interests i WHERE i.event_id = e.id),
	EXISTS(SELECT 1 FROM external_event_interests i WHERE i.event_id = e.id AND i.user_id = $1::uuid),
	e.chat_room_id IS NOT NULL`

func scanEvents(rows pgx.Rows) ([]*Event, error) {
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.CityID, &e.CategoryID, &e.Title, &e.Description, &e.Source, &e.SourceURL,
			&e.VenueName, &e.ImageURL, &e.StartsAt, &e.EndsAt, &e.InterestedCount, &e.IAmInterested, &e.HasGroup); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// List returns active events that haven't ended, soonest first. cityID falls
// back to the caller's own city when empty.
func (r *Repository) List(ctx context.Context, userID, cityID, categoryID string, limit int) ([]*Event, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+eventCols+` FROM external_events e
		WHERE e.active AND COALESCE(e.ends_at, e.starts_at + interval '3 hours') > now()
		  AND e.city_id = COALESCE(NULLIF($2,'')::uuid, (SELECT city_id FROM users WHERE id = $1::uuid))
		  AND (NULLIF($3,'') IS NULL OR e.category_id = NULLIF($3,'')::uuid)
		ORDER BY e.starts_at LIMIT $4`, userID, cityID, categoryID, limit)
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrInvalidInput
		}
		return nil, err
	}
	list, err := scanEvents(rows)
	if isBadUUID(err) {
		return nil, ErrInvalidInput
	}
	return list, err
}

func (r *Repository) Get(ctx context.Context, userID, eventID string) (*Event, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+eventCols+` FROM external_events e WHERE e.id = $2::uuid AND e.active`, userID, eventID)
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	list, err := scanEvents(rows)
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return list[0], nil
}

func (r *Repository) SetInterest(ctx context.Context, userID, eventID string, interested bool) error {
	var err error
	if interested {
		_, err = r.pool.Exec(ctx, `INSERT INTO external_event_interests (event_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, eventID, userID)
	} else {
		_, err = r.pool.Exec(ctx, `DELETE FROM external_event_interests WHERE event_id = $1 AND user_id = $2`, eventID, userID)
	}
	return err
}

func (r *Repository) IsInterested(ctx context.Context, userID, eventID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM external_event_interests WHERE event_id = $1 AND user_id = $2)`, eventID, userID).Scan(&ok)
	return ok, err
}

// InterestedPeople excludes the caller, anyone who opted out of participant
// previews, and anyone with a block in either direction.
func (r *Repository) InterestedPeople(ctx context.Context, userID, eventID string, limit int) ([]Person, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.user_id::text, COALESCE(up.display_name, '')
		FROM external_event_interests i LEFT JOIN user_profiles up ON up.user_id = i.user_id
		WHERE i.event_id = $2 AND i.user_id <> $1::uuid AND COALESCE(up.show_in_participant_previews, true)
		  AND NOT EXISTS (SELECT 1 FROM blocks b
			WHERE (b.user_id = $1::uuid AND b.blocked_user_id = i.user_id) OR (b.user_id = i.user_id AND b.blocked_user_id = $1::uuid))
		ORDER BY i.created_at LIMIT $3`, userID, eventID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.UserID, &p.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClaimRoom atomically records roomID as the event's group room only if none
// is set yet, returning whichever room is now on the event (ours if we won,
// the winner's if we lost the race).
func (r *Repository) ClaimRoom(ctx context.Context, eventID, roomID string) (string, error) {
	var winner string
	err := r.pool.QueryRow(ctx, `
		UPDATE external_events SET chat_room_id = COALESCE(chat_room_id, $2::uuid) WHERE id = $1 RETURNING chat_room_id::text`,
		eventID, roomID).Scan(&winner)
	return winner, err
}

func (r *Repository) ExistingRoom(ctx context.Context, eventID string) (string, error) {
	var room string
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(chat_room_id::text, '') FROM external_events WHERE id = $1 AND active`, eventID).Scan(&room)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return room, err
}
