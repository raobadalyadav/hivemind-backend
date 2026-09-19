package externalevents

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

type Handler struct {
	socialv1.UnimplementedExternalEventServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func evErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrNotFound:
		return status.Error(codes.NotFound, err.Error())
	case ErrNotInterested:
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return status.Error(codes.Internal, fallback)
}

func caller(ctx context.Context) (string, error) {
	id, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "auth required")
	}
	return id, nil
}

func toProto(e *Event) *socialv1.ExternalEvent {
	out := &socialv1.ExternalEvent{
		Id: e.ID, CityId: e.CityID, CategoryId: e.CategoryID, Title: e.Title, Description: e.Description,
		Source: e.Source, SourceUrl: e.SourceURL, VenueName: e.VenueName, ImageUrl: e.ImageURL,
		StartsAt: timestamppb.New(e.StartsAt), InterestedCount: e.InterestedCount,
		IAmInterested: e.IAmInterested, HasGroup: e.HasGroup,
	}
	if e.EndsAt != nil {
		out.EndsAt = timestamppb.New(*e.EndsAt)
	}
	return out
}

func (h *Handler) ListExternalEvents(ctx context.Context, req *socialv1.ListExternalEventsRequest) (*socialv1.ListExternalEventsResponse, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	list, err := h.svc.List(ctx, uid, req.GetCityId(), req.GetCategoryId(), req.GetPageSize())
	if err != nil {
		return nil, evErr(err, "failed to list events")
	}
	out := &socialv1.ListExternalEventsResponse{}
	for _, e := range list {
		out.Events = append(out.Events, toProto(e))
	}
	return out, nil
}

func (h *Handler) GetExternalEvent(ctx context.Context, req *socialv1.GetExternalEventRequest) (*socialv1.ExternalEvent, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	e, err := h.svc.Get(ctx, uid, req.GetEventId())
	if err != nil {
		return nil, evErr(err, "failed to get event")
	}
	return toProto(e), nil
}

func (h *Handler) SetEventInterest(ctx context.Context, req *socialv1.SetEventInterestRequest) (*socialv1.ExternalEvent, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	e, err := h.svc.SetInterest(ctx, uid, req.GetEventId(), req.GetInterested())
	if err != nil {
		return nil, evErr(err, "failed to set interest")
	}
	return toProto(e), nil
}

func (h *Handler) ListInterestedPeople(ctx context.Context, req *socialv1.ListInterestedPeopleRequest) (*socialv1.ListInterestedPeopleResponse, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	people, err := h.svc.ListInterestedPeople(ctx, uid, req.GetEventId())
	if err != nil {
		return nil, evErr(err, "failed to list people")
	}
	out := &socialv1.ListInterestedPeopleResponse{}
	for _, p := range people {
		out.People = append(out.People, &socialv1.InterestedPerson{UserId: p.UserID, DisplayName: p.DisplayName})
	}
	return out, nil
}

func (h *Handler) JoinEventGroup(ctx context.Context, req *socialv1.JoinEventGroupRequest) (*socialv1.JoinEventGroupResponse, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	room, err := h.svc.JoinEventGroup(ctx, uid, req.GetEventId())
	if err != nil {
		return nil, evErr(err, "failed to join event group")
	}
	return &socialv1.JoinEventGroupResponse{RoomId: room}, nil
}
