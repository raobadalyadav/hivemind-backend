package communities

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.CommunityServiceServer. CreateCommunity/
// GetCommunity are real; JoinCommunity/ListCommunityPlans inherit
// socialv1.UnimplementedCommunityServiceServer — see PRD §13.8/§33.
type Handler struct {
	socialv1.UnimplementedCommunityServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateCommunity(ctx context.Context, req *socialv1.CreateCommunityRequest) (*socialv1.Community, error) {
	c := &Community{
		Name:        req.GetName(),
		Description: req.GetDescription(),
		CityID:      req.GetCityId(),
		OwnerID:     req.GetOwnerId(),
	}
	created, err := h.svc.CreateCommunity(ctx, c)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create community")
	}
	return toProto(created), nil
}

func (h *Handler) GetCommunity(ctx context.Context, req *socialv1.GetCommunityRequest) (*socialv1.Community, error) {
	c, err := h.svc.GetCommunity(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "community not found")
	}
	return toProto(c), nil
}

func toProto(c *Community) *socialv1.Community {
	return &socialv1.Community{
		Id:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		CityId:      c.CityID,
		OwnerId:     c.OwnerID,
	}
}
