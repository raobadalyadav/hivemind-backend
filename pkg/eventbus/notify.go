package eventbus

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// EventNotifyUser is the one generic "tell this user something" outbox event
// — waitlist offers, join requests, invites, referral rewards, Meet Again
// all enqueue it in their own transaction, and a single worker handler
// delivers it through notifications.Service. One event type instead of one
// per feature.
const EventNotifyUser = "NOTIFY_USER"

type NotifyUserPayload struct {
	UserID   string `json:"user_id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	DeepLink string `json:"deep_link"`
	Channel  string `json:"channel"` // push | in_app | email; empty = push
}

func EnqueueNotifyUser(ctx context.Context, tx pgx.Tx, p NotifyUserPayload) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return Enqueue(ctx, tx, EventNotifyUser, p.UserID, data)
}
