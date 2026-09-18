package eventbus

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Enqueue writes a domain event row inside the caller's transaction. Call
// this alongside the state-changing INSERT/UPDATE, never on its own — the
// whole point of the outbox is that both writes commit or roll back together.
func Enqueue(ctx context.Context, tx pgx.Tx, eventType string, subjectID string, payload []byte) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO outbox_events (event_type, subject_id, payload) VALUES ($1, $2, $3)`,
		eventType, subjectID, payload,
	)
	return err
}

// Drain publishes unpublished outbox rows and marks them published. Intended
// to be polled on an interval by cmd/worker.
func Drain(ctx context.Context, pool *pgxpool.Pool, publisher *Publisher) error {
	rows, err := pool.Query(ctx,
		`SELECT id, event_type, payload FROM outbox_events WHERE published_at IS NULL ORDER BY created_at LIMIT 100`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		id        string
		eventType string
		payload   []byte
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.eventType, &r.payload); err != nil {
			return err
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range pending {
		if err := publisher.Publish(ctx, r.eventType, r.payload); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET published_at = now() WHERE id = $1`, r.id); err != nil {
			return err
		}
	}
	return nil
}
