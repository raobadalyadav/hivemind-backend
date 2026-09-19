package recommendation

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.RecommendationServiceServer — every RPC is
// fully implemented (see PRD §11/§20 and repository.go's doc comment for
// what "fully implemented" means at this data-maturity stage).
type Handler struct {
	socialv1.UnimplementedRecommendationServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) GetRecommendedPlans(ctx context.Context, req *socialv1.GetRecommendedPlansRequest) (*socialv1.GetRecommendedPlansResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	ids, err := h.svc.GetRecommendedPlans(ctx, userID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to get recommendations")
	}
	return &socialv1.GetRecommendedPlansResponse{PlanIds: ids}, nil
}

func (h *Handler) GetSmartMatch(ctx context.Context, req *socialv1.GetSmartMatchRequest) (*socialv1.GetSmartMatchResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	ids, err := h.svc.GetSmartMatch(ctx, userID, req.GetPlanId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to compute smart match")
	}
	return &socialv1.GetSmartMatchResponse{CompatibleUserIds: ids}, nil
}

func (h *Handler) GetPeopleRecommendations(ctx context.Context, req *socialv1.GetPeopleRecommendationsRequest) (*socialv1.GetPeopleRecommendationsResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	ids, err := h.svc.GetPeopleRecommendations(ctx, callerID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to get people recommendations")
	}
	return &socialv1.GetPeopleRecommendationsResponse{UserIds: ids}, nil
}
