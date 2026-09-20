package grpcmiddleware

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ActiveChecker reports whether a user may still use the API (exists and isn't suspended or deleted).
type ActiveChecker func(ctx context.Context, userID string) (bool, error)

// ActiveUserInterceptor rejects calls from suspended or deleted accounts even while their access
// token is still valid. The answer is cached for ttl so this costs one query per user per ttl, not
// one per call; a lookup error fails open (a database blip must not sign everybody out) but is not cached.
// Place it after AuthUnaryInterceptor.
func ActiveUserInterceptor(check ActiveChecker, ttl time.Duration) grpc.UnaryServerInterceptor {
	type entry struct {
		active  bool
		expires time.Time
	}
	var mu sync.Mutex
	cache := map[string]entry{}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		uid, ok := UserIDFromContext(ctx)
		if !ok || check == nil {
			return handler(ctx, req)
		}
		mu.Lock()
		e, hit := cache[uid]
		mu.Unlock()
		if !hit || time.Now().After(e.expires) {
			active, err := check(ctx, uid)
			if err != nil {
				return handler(ctx, req)
			}
			e = entry{active: active, expires: time.Now().Add(ttl)}
			mu.Lock()
			if len(cache) > 50000 { // bound memory: start over rather than track LRU
				cache = map[string]entry{}
			}
			cache[uid] = e
			mu.Unlock()
		}
		if !e.active {
			return nil, status.Error(codes.Unauthenticated, "this account is not active")
		}
		return handler(ctx, req)
	}
}
