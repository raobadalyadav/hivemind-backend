package chat

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hivemind/backend/pkg/media"
)

var (
	ErrPollNotFound = errors.New("chat: poll not found")
	ErrNotHost      = errors.New("chat: only the plan's host can do that")
)

type meta struct {
	Lat      *float64 `json:"lat,omitempty"`
	Lng      *float64 `json:"lng,omitempty"`
	Label    string   `json:"label,omitempty"`
	Duration int32    `json:"duration,omitempty"`
}

// SendMessage inserts the message and its media rows atomically.
func (r *Repository) SendMessage(ctx context.Context, m *Message) (*Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	out, err := insertMessage(ctx, tx, m)
	if err != nil {
		return nil, err
	}
	for i, a := range m.Media {
		if _, err := tx.Exec(ctx, `
			INSERT INTO message_media (message_id, media_url, media_id, kind, thumb_url, width, height, duration_ms, position)
			VALUES ($1, $2, $3::uuid, $4, $5, $6, $7, $8, $9)`,
			out.ID, a.URL, a.ID, a.Kind, a.ThumbURL, a.Width, a.Height, a.DurationMS, i); err != nil {
			return nil, err
		}
	}
	out.MediaURLs = nil
	for _, a := range m.Media {
		out.MediaURLs = append(out.MediaURLs, a.URL)
	}
	return out, tx.Commit(ctx)
}

func insertMessage(ctx context.Context, tx pgx.Tx, m *Message) (*Message, error) {
	md := meta{Duration: m.DurationSeconds}
	if m.Location != nil {
		md.Lat, md.Lng, md.Label = &m.Location.Latitude, &m.Location.Longitude, m.Location.Label
	}
	raw, _ := json.Marshal(md)
	out := *m
	if out.Type == "" {
		out.Type = "text"
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO messages (room_id, sender_id, body, type, meta) VALUES ($1, $2, $3, $4::message_type, $5::jsonb)
		RETURNING id::text, sent_at`,
		m.RoomID, m.SenderID, m.Body, out.Type, string(raw),
	).Scan(&out.ID, &out.SentAt)
	return &out, err
}

// CreatePoll stores the poll message, poll and options in one transaction.
func (r *Repository) CreatePoll(ctx context.Context, roomID, senderID, question string, options []string) (*Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	msg, err := insertMessage(ctx, tx, &Message{RoomID: roomID, SenderID: senderID, Body: question, Type: "poll"})
	if err != nil {
		return nil, err
	}
	poll := &Poll{Question: question}
	if err := tx.QueryRow(ctx, `INSERT INTO chat_polls (message_id, question) VALUES ($1, $2) RETURNING id::text`,
		msg.ID, question).Scan(&poll.ID); err != nil {
		return nil, err
	}
	for i, label := range options {
		o := PollOption{Label: label}
		if err := tx.QueryRow(ctx, `INSERT INTO chat_poll_options (poll_id, label, position) VALUES ($1, $2, $3) RETURNING id::text`,
			poll.ID, label, i).Scan(&o.ID); err != nil {
			return nil, err
		}
		poll.Options = append(poll.Options, o)
	}
	msg.Poll = poll
	return msg, tx.Commit(ctx)
}

// PollRoom resolves poll → message → room so VotePoll can check membership
// against the room the poll actually lives in (never a client-supplied room).
func (r *Repository) PollRoom(ctx context.Context, pollID string) (string, error) {
	var room string
	err := r.pool.QueryRow(ctx, `
		SELECT m.room_id::text FROM chat_polls p JOIN messages m ON m.id = p.message_id WHERE p.id = $1`, pollID).Scan(&room)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPollNotFound
	}
	return room, err
}

// Vote is a single-statement upsert: one vote per (poll, user), changing it
// just moves the vote. The composite FK rejects an option from another poll.
func (r *Repository) Vote(ctx context.Context, pollID, userID, optionID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO chat_poll_votes (poll_id, user_id, option_id) VALUES ($1, $2, $3)
		ON CONFLICT (poll_id, user_id) DO UPDATE SET option_id = EXCLUDED.option_id, voted_at = now()`,
		pollID, userID, optionID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "22P02") {
		return ErrInvalidInput
	}
	return err
}

func (r *Repository) GetPoll(ctx context.Context, pollID, viewerID string) (*Poll, error) {
	polls, err := r.pollsByID(ctx, []string{pollID}, viewerID, "p.id")
	if err != nil {
		return nil, err
	}
	if p := polls[pollID]; p != nil {
		return p, nil
	}
	return nil, ErrPollNotFound
}

// pollsByID loads polls with counts and the viewer's own vote; keyField picks
// whether ids are poll ids ("p.id") or message ids ("p.message_id"). The map
// is keyed by the id passed in.
func (r *Repository) pollsByID(ctx context.Context, ids []string, viewerID, keyField string) (map[string]*Poll, error) {
	out := map[string]*Poll{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+keyField+`::text, p.id::text, p.question, o.id::text, o.label,
			(SELECT count(*) FROM chat_poll_votes v WHERE v.option_id = o.id),
			COALESCE((SELECT v.option_id::text FROM chat_poll_votes v WHERE v.poll_id = p.id AND v.user_id = $2), '')
		FROM chat_polls p JOIN chat_poll_options o ON o.poll_id = p.id
		WHERE `+keyField+` = ANY($1::uuid[])
		ORDER BY p.id, o.position`, ids, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, pid, question, oid, label, mine string
		var cnt int32
		if err := rows.Scan(&key, &pid, &question, &oid, &label, &cnt, &mine); err != nil {
			return nil, err
		}
		p := out[key]
		if p == nil {
			p = &Poll{ID: pid, Question: question, MyVoteOptionID: mine}
			out[key] = p
		}
		p.Options = append(p.Options, PollOption{ID: oid, Label: label, VoteCount: cnt})
		p.TotalVotes += cnt
	}
	return out, rows.Err()
}

// ListMessages returns the newest messages first, minus anything sent by a
// user the caller has a block with (either direction), fully hydrated.
func (r *Repository) ListMessages(ctx context.Context, roomID, callerID string, limit int) ([]*Message, error) {
	rows, err := r.pool.Query(ctx, msgSelect+`
		WHERE m.room_id = $1
		  AND NOT EXISTS (SELECT 1 FROM blocks b
			WHERE (b.user_id = $2 AND b.blocked_user_id = m.sender_id) OR (b.user_id = m.sender_id AND b.blocked_user_id = $2))
		ORDER BY m.sent_at DESC LIMIT $3`, roomID, callerID, limit)
	if err != nil {
		return nil, err
	}
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	return msgs, r.hydrate(ctx, msgs, callerID)
}

const msgSelect = `SELECT m.id::text, m.room_id::text, m.sender_id::text, m.body, m.sent_at, m.type::text, m.meta FROM messages m `

func scanMessages(rows pgx.Rows) ([]*Message, error) {
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		var m Message
		var raw []byte
		if err := rows.Scan(&m.ID, &m.RoomID, &m.SenderID, &m.Body, &m.SentAt, &m.Type, &raw); err != nil {
			return nil, err
		}
		var md meta
		_ = json.Unmarshal(raw, &md)
		m.DurationSeconds = md.Duration
		if md.Lat != nil && md.Lng != nil {
			m.Location = &Location{Latitude: *md.Lat, Longitude: *md.Lng, Label: md.Label}
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// hydrate attaches media and polls to a page of messages with two queries.
func (r *Repository) hydrate(ctx context.Context, msgs []*Message, viewerID string) error {
	if len(msgs) == 0 {
		return nil
	}
	ids := make([]string, len(msgs))
	byID := make(map[string]*Message, len(msgs))
	for i, m := range msgs {
		ids[i], byID[m.ID] = m.ID, m
	}
	rows, err := r.pool.Query(ctx, `SELECT message_id::text, media_url, COALESCE(media_id::text,''), kind, thumb_url, width, height, duration_ms FROM message_media WHERE message_id = ANY($1::uuid[]) ORDER BY position, id`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var a media.Asset
		if err := rows.Scan(&id, &a.URL, &a.ID, &a.Kind, &a.ThumbURL, &a.Width, &a.Height, &a.DurationMS); err != nil {
			rows.Close()
			return err
		}
		byID[id].MediaURLs = append(byID[id].MediaURLs, a.URL)
		byID[id].Media = append(byID[id].Media, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	polls, err := r.pollsByID(ctx, ids, viewerID, "p.message_id")
	if err != nil {
		return err
	}
	for id, p := range polls {
		byID[id].Poll = p
	}
	return nil
}

// GetMessage loads one hydrated message (nil, nil when absent or blocked).
func (r *Repository) GetMessage(ctx context.Context, messageID, viewerID string) (*Message, error) {
	rows, err := r.pool.Query(ctx, msgSelect+`
		WHERE m.id = $1
		  AND NOT EXISTS (SELECT 1 FROM blocks b
			WHERE (b.user_id = $2 AND b.blocked_user_id = m.sender_id) OR (b.user_id = m.sender_id AND b.blocked_user_id = $2))`,
		messageID, viewerID)
	if err != nil {
		return nil, err
	}
	msgs, err := scanMessages(rows)
	if err != nil || len(msgs) == 0 {
		return nil, err
	}
	return msgs[0], r.hydrate(ctx, msgs, viewerID)
}

func (r *Repository) PinnedMessageID(ctx context.Context, roomID string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(pinned_message_id::text, '') FROM chat_rooms WHERE id = $1`, roomID).Scan(&id)
	return id, err
}

// SetPinned pins a message of THIS room (the EXISTS guard stops pinning a
// message from another room); an empty messageID unpins.
func (r *Repository) SetPinned(ctx context.Context, roomID, messageID string) error {
	if messageID == "" {
		_, err := r.pool.Exec(ctx, `UPDATE chat_rooms SET pinned_message_id = NULL WHERE id = $1`, roomID)
		return err
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE chat_rooms SET pinned_message_id = $2
		WHERE id = $1 AND EXISTS (SELECT 1 FROM messages WHERE id = $2 AND room_id = $1)`, roomID, messageID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMessageNotFound
	}
	return nil
}

// RoomHostID returns the host of the plan a room belongs to; "" for ad-hoc
// rooms (which therefore have no announcer/pinner).
func (r *Repository) RoomHostID(ctx context.Context, roomID string) (string, error) {
	var host string
	err := r.pool.QueryRow(ctx, `
		SELECT p.host_id::text FROM chat_rooms r JOIN plans p ON p.id = r.plan_id WHERE r.id = $1`, roomID).Scan(&host)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return host, err
}

// MessageRoom resolves a message to its room (empty + ErrMessageNotFound when absent).
func (r *Repository) MessageRoom(ctx context.Context, messageID string) (string, error) {
	var room string
	err := r.pool.QueryRow(ctx, `SELECT room_id::text FROM messages WHERE id = $1`, messageID).Scan(&room)
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "22P02") {
		return "", ErrMessageNotFound
	}
	return room, err
}

// CanUsePlanRoom: the plan's host or a confirmed participant.
func (r *Repository) CanUsePlanRoom(ctx context.Context, planID, userID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM plans WHERE id = $1 AND host_id = $2)
		    OR EXISTS(SELECT 1 FROM plan_participants WHERE plan_id = $1 AND user_id = $2 AND status = 'confirmed')`,
		planID, userID).Scan(&ok)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return false, nil
	}
	return ok, err
}
