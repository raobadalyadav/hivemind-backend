package grpcmiddleware

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRateLimiter_BurstThenRefill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rl := NewRateLimiter(ctx, 60) // 1/s, burst 15
	clock := time.Now()
	rl.now = func() time.Time { return clock }

	allowed := 0
	for i := 0; i < 40; i++ {
		if rl.Allow("k") {
			allowed++
		}
	}
	if allowed != 15 {
		t.Fatalf("burst should be 15, got %d", allowed)
	}
	if !rl.Allow("other") {
		t.Fatal("keys are independent")
	}
	clock = clock.Add(3 * time.Second)
	got := 0
	for i := 0; i < 10; i++ {
		if rl.Allow("k") {
			got++
		}
	}
	if got != 3 {
		t.Fatalf("3 seconds refills 3 tokens, got %d", got)
	}
}

func TestPublicRateLimit_OnlyPublicMethodsAndPerIP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rl := NewRateLimiter(ctx, 4) // burst 5
	itc := PublicRateLimitInterceptor(rl, false)
	ok := func(context.Context, any) (any, error) { return "ok", nil }
	pub := &grpc.UnaryServerInfo{FullMethod: "/social.v1.AuthService/RefreshToken"}
	priv := &grpc.UnaryServerInfo{FullMethod: "/social.v1.PlanService/GetPlan"}

	limited := 0
	for i := 0; i < 12; i++ {
		if _, err := itc(context.Background(), nil, pub, ok); status.Code(err) == codes.ResourceExhausted {
			limited++
		}
	}
	if limited != 7 {
		t.Fatalf("12 sign-in style calls with burst 5 → 7 limited, got %d", limited)
	}
	for i := 0; i < 20; i++ {
		if _, err := itc(context.Background(), nil, priv, ok); err != nil {
			t.Fatalf("authenticated methods are not this interceptor's business: %v", err)
		}
	}
}

func TestUserRateLimit_PerUser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rl := NewRateLimiter(ctx, 4)
	itc := UserRateLimitInterceptor(rl)
	ok := func(context.Context, any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/social.v1.PlanService/GetPlan"}
	as := func(u string) context.Context { return context.WithValue(context.Background(), userIDContextKey, u) }

	for i := 0; i < 5; i++ {
		if _, err := itc(as("a"), nil, info, ok); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := itc(as("a"), nil, info, ok); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("user a is over the limit: %v", err)
	}
	if _, err := itc(as("b"), nil, info, ok); err != nil {
		t.Fatalf("user b is unaffected: %v", err)
	}
}

func TestActiveUserInterceptor_BlocksInactiveAndCaches(t *testing.T) {
	calls := 0
	active := map[string]bool{"good": true, "gone": false}
	itc := ActiveUserInterceptor(func(_ context.Context, id string) (bool, error) {
		calls++
		if id == "boom" {
			return false, context.DeadlineExceeded
		}
		return active[id], nil
	}, time.Minute)
	ok := func(context.Context, any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/social.v1.PlanService/GetPlan"}
	as := func(u string) context.Context { return context.WithValue(context.Background(), userIDContextKey, u) }

	if _, err := itc(as("good"), nil, info, ok); err != nil {
		t.Fatal(err)
	}
	itc(as("good"), nil, info, ok)
	if calls != 1 {
		t.Fatalf("the answer is cached, calls=%d", calls)
	}
	if _, err := itc(as("gone"), nil, info, ok); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("a suspended/deleted user is refused: %v", err)
	}
	if _, err := itc(as("boom"), nil, info, ok); err != nil {
		t.Fatalf("a lookup error fails open: %v", err)
	}
	if _, err := itc(context.Background(), nil, info, ok); err != nil {
		t.Fatalf("public calls (no user) pass through: %v", err)
	}
}
