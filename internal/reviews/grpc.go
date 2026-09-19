package reviews

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.ReviewServiceServer — every RPC is fully
// implemented (see PRD §13.11).
type Handler struct {
	socialv1.UnimplementedReviewServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateReview(ctx context.Context, req *socialv1.CreateReviewRequest) (*socialv1.Review, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	rv, err := h.svc.CreateReview(ctx, req.GetBookingId(), callerID, req.GetRating(), req.GetComment())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrBookingNotAttended:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to create review")
		}
	}
	return toProto(rv), nil
}

func (h *Handler) GetPlanReviews(ctx context.Context, req *socialv1.GetPlanReviewsRequest) (*socialv1.GetPlanReviewsResponse, error) {
	list, avg, err := h.svc.GetPlanReviews(ctx, req.GetPlanId())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load reviews")
	}
	out := make([]*socialv1.Review, 0, len(list))
	for _, rv := range list {
		out = append(out, toProto(rv))
	}
	return &socialv1.GetPlanReviewsResponse{Reviews: out, AvgRating: avg}, nil
}

func toProto(rv *Review) *socialv1.Review {
	return &socialv1.Review{
		Id:      rv.ID,
		PlanId:  rv.PlanID,
		UserId:  rv.UserID,
		Rating:  rv.Rating,
		Comment: rv.Comment,
	}
}
