// Package db builds the Postgres connection pool the API and worker share.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool connects with explicit limits instead of library defaults: a bounded pool (so a traffic spike
// queues instead of exhausting Postgres connections), recycled connections (so a failover or a
// load-balancer idle cut doesn't leave dead ones), and a periodic health check.
func NewPool(ctx context.Context, url string, maxConns int) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = int32(maxConns)
	cfg.MinConns = min(int32(2), cfg.MaxConns)
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnLifetimeJitter = 5 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	return pgxpool.NewWithConfig(ctx, cfg)
}
