package smartgroups

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.SmartGroupServiceServer — every RPC is fully
// implemented (see flow.md §12).
type Handler struct {
	socialv1.UnimplementedSmartGroupServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) GenerateSmartGroups(ctx context.Context, req *socialv1.GenerateSmartGroupsRequest) (*socialv1.GenerateSmartGroupsResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	groups, err := h.svc.GenerateSmartGroups(ctx, req.GetPlanId(), callerID, role)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrTooManyPeople:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to generate smart groups")
		}
	}
	return toResponse(groups), nil
}

func (h *Handler) ListSmartGroups(ctx context.Context, req *socialv1.ListSmartGroupsRequest) (*socialv1.GenerateSmartGroupsResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	groups, err := h.svc.ListSmartGroups(ctx, req.GetPlanId(), callerID, role)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to list smart groups")
		}
	}
	return toResponse(groups), nil
}

func toResponse(groups []*Group) *socialv1.GenerateSmartGroupsResponse {
	out := make([]*socialv1.PlanGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, &socialv1.PlanGroup{
			Id:            g.ID,
			ChatRoomId:    g.ChatRoomID,
			Label:         g.Label,
			MemberUserIds: g.MemberIDs,
		})
	}
	return &socialv1.GenerateSmartGroupsResponse{Groups: out}
}
