package recommendation

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.RecommendationServiceServer. GetRecommendedPlans
// is real (cold-start/editorial only, see repository.go doc); GetSmartMatch
// inherits socialv1.UnimplementedRecommendationServiceServer — see PRD §11.
type Handler struct {
	socialv1.UnimplementedRecommendationServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) GetRecommendedPlans(ctx context.Context, req *socialv1.GetRecommendedPlansRequest) (*socialv1.GetRecommendedPlansResponse, error) {
	ids, err := h.svc.GetRecommendedPlans(ctx, req.GetUserId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to get recommendations")
	}
	return &socialv1.GetRecommendedPlansResponse{PlanIds: ids}, nil
}
