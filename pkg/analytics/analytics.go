// Package analytics records funnel events (PRD §24) to the events table.
// Wired at the minimum number of choke points needed for real numbers —
// see internal/auth's issueTokens and cmd/worker's booking handlers — not
// scattered across every RPC handler.
package analytics

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Recorder struct {
	pool *pgxpool.Pool
}

func NewRecorder(pool *pgxpool.Pool) *Recorder {
	return &Recorder{pool: pool}
}

func (r *Recorder) Record(ctx context.Context, userID, eventName string, properties map[string]any) error {
	props, err := json.Marshal(properties)
	if err != nil {
		return err
	}
	// props passed as string, not []byte — pgx sends a Go string as `text`,
	// and text→jsonb is a normal cast; bytea→jsonb is not.
	_, err = r.pool.Exec(ctx,
		`INSERT INTO events (user_id, event_name, properties) VALUES (NULLIF($1,'')::uuid, $2, $3::jsonb)`,
		userID, eventName, string(props),
	)
	return err
}
