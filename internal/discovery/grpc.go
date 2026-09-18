package discovery

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.DiscoveryServiceServer — every RPC is fully
// implemented (see PRD §13.3).
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

var feedSectionNames = map[socialv1.FeedSection]string{
	socialv1.FeedSection_TODAY:    "TODAY",
	socialv1.FeedSection_TONIGHT:  "TONIGHT",
	socialv1.FeedSection_WEEKEND:  "WEEKEND",
	socialv1.FeedSection_NEAR_YOU: "NEAR_YOU",
	socialv1.FeedSection_FOR_YOU:  "FOR_YOU",
}

func (h *Handler) GetHomeFeed(ctx context.Context, req *socialv1.GetHomeFeedRequest) (*socialv1.GetHomeFeedResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	section := feedSectionNames[req.GetSection()]
	ids, err := h.svc.GetHomeFeed(ctx, userID, section)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to load home feed")
	}
	return &socialv1.GetHomeFeedResponse{PlanIds: ids}, nil
}

func (h *Handler) DetectCity(ctx context.Context, req *socialv1.DetectCityRequest) (*socialv1.DetectCityResponse, error) {
	if req.GetLocation() == nil {
		return nil, status.Error(codes.InvalidArgument, "location is required")
	}
	id, name, err := h.svc.DetectCity(ctx, req.GetLocation().GetLatitude(), req.GetLocation().GetLongitude())
	if err != nil {
		if err == ErrCityNotFound {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to detect city")
	}
	return &socialv1.DetectCityResponse{CityId: id, CityName: name}, nil
}
