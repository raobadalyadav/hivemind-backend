package grpcmiddleware

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/hivemind/backend/pkg/security"
)

func callAs(t *testing.T, issuer *security.TokenIssuer, role, method string) error {
	t.Helper()
	tok, _, err := issuer.Issue("u1", role)
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", tok))
	_, err = AuthUnaryInterceptor(issuer)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, any) (any, error) { return "ok", nil })
	return err
}

func TestMoneyAndStaffRPCsNeedAnAdminRole(t *testing.T) {
	issuer := security.NewTokenIssuer("test-secret-test-secret-test-secret", time.Hour)
	for _, m := range []string{
		"/social.v1.PaymentService/RefundPayment", // moves money
		"/social.v1.ModerationService/ResolveCase",
		"/social.v1.AdminService/SuspendUser",
		"/social.v1.NotificationService/SendNotification",
	} {
		if err := callAs(t, issuer, "user", m); status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s must refuse a normal user, got %v", m, err)
		}
		if err := callAs(t, issuer, "admin", m); err != nil {
			t.Errorf("%s must allow an admin, got %v", m, err)
		}
	}
	if err := callAs(t, issuer, "user", "/social.v1.PlanService/GetPlan"); err != nil {
		t.Errorf("ordinary RPCs stay open to signed-in users: %v", err)
	}
	if _, _, err := issuer.Issue("u", "user"); err != nil || !IsAdminRole("super_admin") || IsAdminRole("user") {
		t.Error("IsAdminRole")
	}
}
