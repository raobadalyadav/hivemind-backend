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

const (
	userIDContextKey contextKey = "user_id"
	roleContextKey   contextKey = "role"
)

// publicMethods lists full gRPC method names (service/Method) that don't require a token.
var publicMethods = map[string]bool{
	"/social.v1.AuthService/SignInWithGoogle":       true,
	"/social.v1.AuthService/SignInWithApple":        true,
	"/social.v1.AuthService/RefreshToken":           true,
	"/social.v1.AuthService/RequestAccountRecovery": true,
	"/social.v1.AuthService/RecoverAccount":         true,
	"/grpc.health.v1.Health/Check":                  true,
	"/grpc.health.v1.Health/Watch":                  true,
}

// adminMethods lists full gRPC method names restricted to admin/super_admin
// roles — the RBAC gate PRD §23 calls for on the admin surface, plus any
// other RPC that isn't self-referential (acts on/sends to someone other than
// the caller) and would otherwise let any authenticated user act as an
// admin without being one.
var adminMethods = map[string]bool{
	"/social.v1.AdminService/ListUsers":               true,
	"/social.v1.AdminService/SuspendUser":             true,
	"/social.v1.AdminService/ListReports":             true,
	"/social.v1.AdminService/OverrideBookingStatus":   true,
	"/social.v1.AdminService/GetDashboardStats":       true,
	"/social.v1.NotificationService/SendNotification": true,
	"/social.v1.ModerationService/ResolveCase":        true,
	"/social.v1.AdminService/ApproveHost":             true,
	"/social.v1.AdminService/MarkPayoutProcessed":     true,
	"/social.v1.AdminService/AdminGrantCredit":        true,
	"/social.v1.AdminService/CreateCoupon":            true,
	"/social.v1.AdminService/ListCoupons":             true,
	"/social.v1.AdminService/DeactivateCoupon":        true,
	"/social.v1.AdminService/CreateCity":              true,
	"/social.v1.AdminService/UpdateCityStatus":        true,
}

func isAdminRole(role string) bool {
	return role == "admin" || role == "super_admin"
}

func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDContextKey).(string)
	return id, ok
}

func RoleFromContext(ctx context.Context) (string, bool) {
	role, ok := ctx.Value(roleContextKey).(string)
	return role, ok
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

		if adminMethods[info.FullMethod] && !isAdminRole(claims.Role) {
			return nil, status.Error(codes.PermissionDenied, "admin role required")
		}

		ctx = context.WithValue(ctx, userIDContextKey, claims.UserID)
		ctx = context.WithValue(ctx, roleContextKey, claims.Role)
		return handler(ctx, req)
	}
}
