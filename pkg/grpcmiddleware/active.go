package grpcmiddleware

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AccountState is what the API needs to know about a caller on every request.
type AccountState struct {
	Active bool // exists and isn't suspended or deleted
	Adult  bool // has set an 18+ date of birth (age_verified)
	Agreed bool // accepted the current Terms + Privacy Policy
}

// ActiveChecker looks up the caller's AccountState.
type ActiveChecker func(ctx context.Context, userID string) (AccountState, error)

// ageGateOpen lists what an account without a verified 18+ birthday may still do: read and complete
// its own user record, sign out, delete the account, read its own profile — nothing else.
var ageGateOpen = map[string]bool{
	"/social.v1.UserService/GetUser":                true,
	"/social.v1.UserService/UpdateUser":             true,
	"/social.v1.UserService/AcceptTerms":            true,
	"/social.v1.UserService/DeleteAccount":          true,
	"/social.v1.UserService/ExportMyData":           true,
	"/social.v1.UserService/RegisterDevice":         true,
	"/social.v1.ProfileService/GetProfile":          true,
	"/social.v1.NotificationService/GetUnreadCount": true,
}

// ActiveUserInterceptor rejects calls from suspended or deleted accounts, and from accounts that haven't
// set an 18+ birthday (except the calls in ageGateOpen), even while their access
// token is still valid. The answer is cached for ttl so this costs one query per user per ttl, not
// one per call; a lookup error fails open (a database blip must not sign everybody out) but is not cached.
// Place it after AuthUnaryInterceptor.
func ActiveUserInterceptor(check ActiveChecker, ttl time.Duration) grpc.UnaryServerInterceptor {
	type entry struct {
		state   AccountState
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
			active, err := check(ctx, uid) // named for history: it is an AccountState
			if err != nil {
				return handler(ctx, req)
			}
			e = entry{state: active, expires: time.Now().Add(ttl)}
			if !active.Adult || !active.Agreed {
				e.expires = time.Now().Add(2 * time.Second) // it flips the moment they set a birthday: re-check soon
			}
			mu.Lock()
			if len(cache) > 50000 { // bound memory: start over rather than track LRU
				cache = map[string]entry{}
			}
			cache[uid] = e
			mu.Unlock()
		}
		if !e.state.Active {
			return nil, status.Error(codes.Unauthenticated, "this account is not active")
		}
		if !ageGateOpen[info.FullMethod] {
			if !e.state.Adult {
				return nil, status.Error(codes.FailedPrecondition, "add your date of birth (18+) to continue")
			}
			if !e.state.Agreed {
				return nil, status.Error(codes.FailedPrecondition, "accept the Terms of Service and Privacy Policy to continue")
			}
		}
		resp, err := handler(ctx, req)
		if ageGateOpen[info.FullMethod] && (!e.state.Adult || !e.state.Agreed) {
			mu.Lock()
			delete(cache, uid) // the call may have just set the birthday or accepted the terms: re-check on the next one
			mu.Unlock()
		}
		return resp, err
	}
}
