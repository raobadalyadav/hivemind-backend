package discovery

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.DiscoveryServiceServer. GetNearbyPlans is real;
// GetHomeFeed inherits socialv1.UnimplementedDiscoveryServiceServer — see
// PRD §13.3.
type Handler struct {
	socialv1.UnimplementedDiscoveryServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) GetNearbyPlans(ctx context.Context, req *socialv1.GetNearbyPlansRequest) (*socialv1.GetNearbyPlansResponse, error) {
	if req.GetOrigin() == nil {
		return nil, status.Error(codes.InvalidArgument, "origin is required")
	}
	ids, err := h.svc.GetNearbyPlans(ctx, req.GetOrigin().GetLatitude(), req.GetOrigin().GetLongitude(), req.GetRadiusKm())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to search nearby plans")
	}
	return &socialv1.GetNearbyPlansResponse{PlanIds: ids}, nil
}
