package grpcmiddleware

import (
	"context"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// RateLimiter is an in-process token bucket per key. It protects a single API instance
// (each replica keeps its own buckets, so the effective ceiling is limit × replicas —
// move to Redis if a hard global ceiling ever matters).
type RateLimiter struct {
	perMinute float64
	burst     float64
	now       func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter allows perMinute calls per key on average, with a burst of a quarter minute's worth
// (at least 5). The janitor stops when ctx is done.
func NewRateLimiter(ctx context.Context, perMinute int) *RateLimiter {
	rl := &RateLimiter{perMinute: float64(perMinute), burst: max(float64(perMinute)/4, 5), now: time.Now, buckets: map[string]*bucket{}}
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rl.evictIdle()
			}
		}
	}()
	return rl
}

// Allow takes one token for key; false means the caller is over the limit.
func (r *RateLimiter) Allow(key string) bool {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[key]
	if !ok {
		b = &bucket{tokens: r.burst, last: now}
		r.buckets[key] = b
	}
	b.tokens = min(r.burst, b.tokens+now.Sub(b.last).Seconds()*r.perMinute/60)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictIdle drops buckets that have fully refilled (nothing to remember about them).
func (r *RateLimiter) evictIdle() {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, b := range r.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*r.perMinute/60 >= r.burst {
			delete(r.buckets, k)
		}
	}
}

var errRateLimited = status.Error(codes.ResourceExhausted, "too many requests — slow down and try again in a moment")

// ClientIP is the caller's address; with trustProxy the first X-Forwarded-For hop is used
// (only enable behind a proxy that sets it, or clients could pick their own bucket).
func ClientIP(ctx context.Context, trustProxy bool) string {
	if trustProxy {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if v := md.Get("x-forwarded-for"); len(v) > 0 {
				if first, _, _ := strings.Cut(v[0], ","); strings.TrimSpace(first) != "" {
					return strings.TrimSpace(first)
				}
			}
		}
	}
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		addr := p.Addr.String()
		if i := strings.LastIndex(addr, ":"); i > 0 {
			return addr[:i]
		}
		return addr
	}
	return "unknown"
}

// PublicRateLimitInterceptor limits the calls that need no token (sign-in, refresh, recovery) per client IP —
// the brute-force / abuse surface. Place it before AuthUnaryInterceptor.
func PublicRateLimitInterceptor(rl *RateLimiter, trustProxy bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if rl != nil && publicMethods[info.FullMethod] && !strings.HasPrefix(info.FullMethod, "/grpc.health.") {
			if !rl.Allow("ip:" + ClientIP(ctx, trustProxy)) {
				return nil, errRateLimited
			}
		}
		return handler(ctx, req)
	}
}

// UserRateLimitInterceptor limits every authenticated call per user. Place it after AuthUnaryInterceptor.
func UserRateLimitInterceptor(rl *RateLimiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if rl != nil {
			if uid, ok := UserIDFromContext(ctx); ok && !rl.Allow("user:"+uid) {
				return nil, errRateLimited
			}
		}
		return handler(ctx, req)
	}
}
