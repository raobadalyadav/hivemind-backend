package chat

import (
	"strings"

	"context"
	"errors"
	"github.com/hivemind/backend/internal/notifications"
	"time"
)

var ErrNotConnected = errors.New("chat: you can message someone once you're connected")

type Summary struct {
	RoomID, Kind, Title, OtherUserID, PlanID, AvatarURL, LastMessage string
	OtherVerified, Muted                                             bool
	LastMessageAt                                                    *time.Time
	Unread                                                           int32
	Primary                                                          bool
}

// Inbox returns every chat the user is in, newest activity first, already
// classified. Primary = 1:1 chats with friends + chats of plans that haven't
// ended (and weren't cancelled); General = everything else. Rooms whose only
// other person is blocked (either way) are hidden, and their messages don't
// count as unread.
func (r *Repository) Inbox(ctx context.Context, userID string, now time.Time) ([]Summary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id::text, r.kind, COALESCE(r.plan_id::text, ''),
			CASE r.kind
				WHEN 'dm' THEN COALESCE(o_up.display_name, '')
				WHEN 'plan' THEN COALESCE(p.title, '')
				ELSE COALESCE((SELECT string_agg(x.display_name, ', ') FROM (
						SELECT up.display_name FROM chat_members m2 JOIN user_profiles up ON up.user_id = m2.user_id
						WHERE m2.room_id = r.id AND m2.user_id <> $1::uuid ORDER BY m2.joined_at LIMIT 3) x), '')
			END AS title,
			COALESCE(o.user_id::text, ''),
			COALESCE((SELECT ph.thumb_url FROM profile_photos ph WHERE ph.user_id = o.user_id ORDER BY position, created_at LIMIT 1), ''),
			COALESCE(o_up.selfie_verified_at IS NOT NULL, false),
			CASE WHEN lm.type = 'image' THEN '📷 Photo'
			     WHEN lm.type = 'poll' THEN '📊 Poll'
			     WHEN lm.type = 'location' THEN '📍 Location'
			     WHEN lm.type = 'voice' THEN '🎤 Voice message'
			     WHEN lm.type = 'announcement' THEN '📢 ' || left(lm.body, 80)
			     ELSE left(COALESCE(lm.body, ''), 90) END,
			lm.sent_at, cm.muted,
			(SELECT count(*) FROM messages m WHERE m.room_id = r.id AND m.sender_id <> $1::uuid AND m.sent_at > cm.last_read_at
				AND NOT EXISTS (SELECT 1 FROM blocks b WHERE (b.user_id = $1::uuid AND b.blocked_user_id = m.sender_id)
				                                          OR (b.user_id = m.sender_id AND b.blocked_user_id = $1::uuid)))::int,
			(r.kind = 'dm' OR (r.kind = 'plan' AND p.status IN ('published','full') AND p.ends_at > $2::timestamptz)) AS is_primary
		FROM chat_members cm
		JOIN chat_rooms r ON r.id = cm.room_id
		LEFT JOIN plans p ON p.id = r.plan_id
		LEFT JOIN chat_members o ON r.kind = 'dm' AND o.room_id = r.id AND o.user_id <> $1::uuid
		LEFT JOIN user_profiles o_up ON o_up.user_id = o.user_id
		LEFT JOIN LATERAL (SELECT body, type::text AS type, sent_at FROM messages WHERE room_id = r.id ORDER BY sent_at DESC LIMIT 1) lm ON true
		WHERE cm.user_id = $1::uuid
		  AND NOT (r.kind = 'dm' AND EXISTS (SELECT 1 FROM blocks b
				WHERE (b.user_id = $1::uuid AND b.blocked_user_id = o.user_id) OR (b.user_id = o.user_id AND b.blocked_user_id = $1::uuid)))
		ORDER BY lm.sent_at DESC NULLS LAST, r.created_at DESC
		LIMIT 200`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.RoomID, &s.Kind, &s.PlanID, &s.Title, &s.OtherUserID, &s.AvatarURL, &s.OtherVerified,
			&s.LastMessage, &s.LastMessageAt, &s.Muted, &s.Unread, &s.Primary); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MarkRead moves the member's read marker to now (member-only).
func (r *Repository) MarkRead(ctx context.Context, roomID, userID string, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE chat_members SET last_read_at = $3::timestamptz WHERE room_id = $1 AND user_id = $2`, roomID, userID, now)
	if err != nil {
		if isBadUUID(err) {
			return ErrNotAMember
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotAMember
	}
	// Reading the chat clears its "new message" notification too.
	_, _ = r.pool.Exec(ctx, `UPDATE notifications SET read = true WHERE user_id = $1 AND type = 'chat_message' AND target_id = $2 AND NOT read`, userID, roomID)
	return nil
}

// notifyDM tells the other person in a 1:1 chat about a new message (unless they muted it);
// while an earlier message notification is unread, further messages collapse into it.
func (r *Repository) notifyDM(ctx context.Context, m *Message) {
	rows, err := r.pool.Query(ctx, `
		SELECT cm.user_id::text FROM chat_rooms cr JOIN chat_members cm ON cm.room_id = cr.id
		WHERE cr.id = $1 AND cr.kind = 'dm' AND cm.user_id <> $2 AND NOT cm.muted`, m.RoomID, m.SenderID)
	if err != nil {
		return
	}
	var to []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			to = append(to, id)
		}
	}
	rows.Close()
	body := strings.TrimSpace(m.Body)
	if r := []rune(body); len(r) > 80 {
		body = string(r[:80]) + "…"
	}
	if body == "" {
		body = "Sent an attachment"
	}
	for _, u := range to {
		_, _ = notifications.Emit(ctx, r.pool, notifications.Event{
			UserID: u, ActorID: m.SenderID, Type: notifications.TypeChatMessage, TargetID: m.RoomID,
			Title: "{actor} sent you a message", Body: body, DeepLink: "hivemind://chat/" + m.RoomID,
			DedupeKey: "chat:" + m.RoomID,
		})
	}
}

// AreConnected: an accepted connection either way, and no block.
func (r *Repository) AreConnected(ctx context.Context, a, b string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM connections c WHERE c.status = 'accepted'::connection_status
			AND ((c.requester_id = $1 AND c.recipient_id = $2) OR (c.requester_id = $2 AND c.recipient_id = $1)))
		   AND NOT EXISTS (SELECT 1 FROM blocks k WHERE (k.user_id = $1 AND k.blocked_user_id = $2) OR (k.user_id = $2 AND k.blocked_user_id = $1))`,
		a, b).Scan(&ok)
	if isBadUUID(err) {
		return false, nil
	}
	return ok, err
}

// OpenDM returns the pair's single DM room, creating it (and both memberships)
// on first use. dm_key is UNIQUE, so concurrent callers converge on one room.
func (r *Repository) OpenDM(ctx context.Context, a, b string) (string, error) {
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO chat_rooms (kind, dm_key) VALUES ('dm', $1)
		ON CONFLICT (dm_key) DO UPDATE SET dm_key = EXCLUDED.dm_key RETURNING id::text`, lo+":"+hi).Scan(&id); err != nil {
		return "", err
	}
	for _, u := range []string{a, b} {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_members (room_id, user_id) VALUES ($1, $2) ON CONFLICT (room_id, user_id) DO NOTHING`, id, u); err != nil {
			return "", err
		}
	}
	return id, tx.Commit(ctx)
}

// DMBlocked: the room is a DM and one participant has blocked the other. The
// sender is told they're not a member (no hint that they were blocked).
func (r *Repository) DMBlocked(ctx context.Context, roomID, senderID string) (bool, error) {
	var blocked bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM chat_rooms r JOIN chat_members o ON o.room_id = r.id AND o.user_id <> $2::uuid
			WHERE r.id = $1::uuid AND r.kind = 'dm'
			  AND EXISTS (SELECT 1 FROM blocks b WHERE (b.user_id = $2::uuid AND b.blocked_user_id = o.user_id)
			                                        OR (b.user_id = o.user_id AND b.blocked_user_id = $2::uuid)))`, roomID, senderID).Scan(&blocked)
	return blocked, err
}
