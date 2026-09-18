package search

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.SearchServiceServer — its one RPC, SearchText,
// is fully implemented (see PRD §20).
type Handler struct {
	socialv1.UnimplementedSearchServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) SearchText(ctx context.Context, req *socialv1.SearchTextRequest) (*socialv1.SearchTextResponse, error) {
	ids, err := h.svc.SearchText(ctx, req.GetQuery(), req.GetCityId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "search failed")
	}
	return &socialv1.SearchTextResponse{PlanIds: ids}, nil
}
