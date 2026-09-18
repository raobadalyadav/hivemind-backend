package admin

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.AdminServiceServer. ListUsers/SuspendUser are
// real; ListReports/OverrideBookingStatus/GetDashboardStats inherit
// socialv1.UnimplementedAdminServiceServer — see PRD §13.18/§23.
//
// RBAC (restricting these RPCs to admin-role callers) is not yet enforced —
// TODO(phase1): add a role claim to security.Claims and check it here or in
// a dedicated admin interceptor before this ships past the scaffold stage.
type Handler struct {
	socialv1.UnimplementedAdminServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) ListUsers(ctx context.Context, req *socialv1.ListUsersRequest) (*socialv1.ListUsersResponse, error) {
	ids, err := h.svc.ListUsers(ctx, req.GetQuery())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list users")
	}
	return &socialv1.ListUsersResponse{UserIds: ids}, nil
}

func (h *Handler) SuspendUser(ctx context.Context, req *socialv1.SuspendUserRequest) (*socialv1.SuspendUserResponse, error) {
	err := h.svc.SuspendUser(ctx, req.GetUserId(), req.GetReason(), req.GetActorId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to suspend user")
	}
	return &socialv1.SuspendUserResponse{}, nil
}
