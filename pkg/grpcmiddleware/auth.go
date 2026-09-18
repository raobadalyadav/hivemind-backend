// Package grpcmiddleware provides the shared unary interceptor chain (auth,
// logging, recovery) applied uniformly in cmd/api/main.go — auth is verified
// once here, not re-implemented per service.
package grpcmiddleware

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/hivemind/backend/pkg/security"
)

type contextKey string

const userIDContextKey contextKey = "user_id"

// publicMethods lists full gRPC method names (service/Method) that don't require a token.
var publicMethods = map[string]bool{
	"/social.v1.AuthService/SignUp":               true,
	"/social.v1.AuthService/SignIn":               true,
	"/social.v1.AuthService/RefreshToken":         true,
	"/social.v1.AuthService/RequestPasswordReset": true,
	"/grpc.health.v1.Health/Check":                true,
	"/grpc.health.v1.Health/Watch":                true,
}

func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDContextKey).(string)
	return id, ok
}

func AuthUnaryInterceptor(issuer *security.TokenIssuer) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if publicMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		tokens := md.Get("authorization")
		if len(tokens) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization token")
		}

		claims, err := issuer.Verify(tokens[0])
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		ctx = context.WithValue(ctx, userIDContextKey, claims.UserID)
		return handler(ctx, req)
	}
}
