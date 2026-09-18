// Package idempotency guards mutation RPCs (bookings, payments, refunds —
// PRD §17) against duplicate execution on retry, using a Redis SETNX lock
// keyed by a caller-supplied idempotency key.
package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrDuplicateRequest = errors.New("idempotency: request already processed")

const lockTTL = 24 * time.Hour

type Guard struct {
	rdb *redis.Client
}

func NewGuard(rdb *redis.Client) *Guard {
	return &Guard{rdb: rdb}
}

// Reserve claims the key for this request. Returns ErrDuplicateRequest if the
// key was already reserved (i.e. a prior attempt is in flight or completed).
func (g *Guard) Reserve(ctx context.Context, key string) error {
	if key == "" {
		return nil // no idempotency key supplied, caller opted out
	}
	ok, err := g.rdb.SetNX(ctx, "idempotency:"+key, "1", lockTTL).Result()
	if err != nil {
		return err
	}
	if !ok {
		return ErrDuplicateRequest
	}
	return nil
}
