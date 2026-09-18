package admin

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.AdminServiceServer — every RPC is fully
// implemented (see PRD §13.18/§23). RBAC is enforced by
// pkg/grpcmiddleware's adminMethods gate (checked before any handler here
// runs), not by this package.
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
	// actor_id comes from the authenticated admin's own token, not the
	// request — otherwise one admin could frame another in the audit log.
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	err := h.svc.SuspendUser(ctx, req.GetUserId(), req.GetReason(), actorID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to suspend user")
	}
	return &socialv1.SuspendUserResponse{}, nil
}

func (h *Handler) ListReports(ctx context.Context, req *socialv1.ListReportsRequest) (*socialv1.ListReportsResponse, error) {
	ids, err := h.svc.ListReports(ctx, req.GetStatus())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list reports")
	}
	return &socialv1.ListReportsResponse{CaseIds: ids}, nil
}

func (h *Handler) OverrideBookingStatus(ctx context.Context, req *socialv1.OverrideBookingStatusRequest) (*socialv1.OverrideBookingStatusResponse, error) {
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	err := h.svc.OverrideBookingStatus(ctx, req.GetBookingId(), req.GetNewStatus(), req.GetReason(), actorID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, "invalid booking id or status value")
	}
	return &socialv1.OverrideBookingStatusResponse{}, nil
}

func (h *Handler) GetDashboardStats(ctx context.Context, req *socialv1.GetDashboardStatsRequest) (*socialv1.GetDashboardStatsResponse, error) {
	stats, err := h.svc.GetDashboardStats(ctx, req.GetCityId())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load dashboard stats")
	}
	return &socialv1.GetDashboardStatsResponse{
		BookingsToday: stats.BookingsToday,
		GmvMinorUnits: stats.GMVMinorUnits,
	}, nil
}
