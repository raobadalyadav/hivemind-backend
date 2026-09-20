package connections

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
	"github.com/hivemind/backend/pkg/idempotency"
)

// Handler implements socialv1.ConnectionServiceServer — every RPC is fully
// implemented (see PRD §13.9/§33).
type Handler struct {
	socialv1.UnimplementedConnectionServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RequestConnection(ctx context.Context, req *socialv1.RequestConnectionRequest) (*socialv1.Connection, error) {
	requesterID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	c := &Connection{
		RequesterID:  requesterID,
		RecipientID:  req.GetRecipientId(),
		OriginPlanID: req.GetOriginPlanId(),
	}
	created, err := h.svc.RequestConnection(ctx, c)
	if err != nil {
		return nil, connErr(err, "failed to request connection")
	}
	return toProto(created), nil
}

func (h *Handler) ListConnections(ctx context.Context, req *socialv1.ListConnectionsRequest) (*socialv1.ListConnectionsResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	st := map[socialv1.ConnectionStatus]string{socialv1.ConnectionStatus_PENDING: "pending", socialv1.ConnectionStatus_ACCEPTED: "accepted", socialv1.ConnectionStatus_REJECTED: "rejected"}[req.GetStatus()]
	dir := map[socialv1.ConnectionDirection]string{socialv1.ConnectionDirection_CONNECTION_DIRECTION_INCOMING: "incoming", socialv1.ConnectionDirection_CONNECTION_DIRECTION_OUTGOING: "outgoing"}[req.GetDirection()]
	list, err := h.svc.ListConnections(ctx, userID, st, dir)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to list connections")
	}
	out := make([]*socialv1.Connection, 0, len(list))
	for _, c := range list {
		out = append(out, toProto(c))
	}
	return &socialv1.ListConnectionsResponse{Connections: out}, nil
}

func (h *Handler) RespondConnection(ctx context.Context, req *socialv1.RespondConnectionRequest) (*socialv1.Connection, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	c, err := h.svc.RespondConnection(ctx, req.GetConnectionId(), callerID, req.GetAccept())
	if err != nil {
		return nil, connErr(err, "failed to respond to connection")
	}
	return toProto(c), nil
}

var statusToProtoMap = map[string]socialv1.ConnectionStatus{
	"pending":  socialv1.ConnectionStatus_PENDING,
	"accepted": socialv1.ConnectionStatus_ACCEPTED,
	"rejected": socialv1.ConnectionStatus_REJECTED,
}

func toProto(c *Connection) *socialv1.Connection {
	st, ok := statusToProtoMap[c.Status]
	if !ok {
		st = socialv1.ConnectionStatus_CONNECTION_STATUS_UNSPECIFIED
	}
	return &socialv1.Connection{
		Id:           c.ID,
		RequesterId:  c.RequesterID,
		RecipientId:  c.RecipientID,
		OriginPlanId: c.OriginPlanID,
		Status:       st,
	}
}

func connErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput, ErrBadOriginPlan:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrForbidden, ErrNotAttendee, ErrInviteeRules:
		return status.Error(codes.PermissionDenied, err.Error())
	case ErrConnectionNotFound, ErrUserNotFound:
		return status.Error(codes.NotFound, err.Error())
	case ErrAlreadyDecided:
		return status.Error(codes.FailedPrecondition, err.Error())
	case idempotency.ErrDuplicateRequest:
		return status.Error(codes.AlreadyExists, err.Error())
	}
	return status.Error(codes.Internal, fallback)
}

var stateToProto = map[string]socialv1.ConnectionState{
	"none":      socialv1.ConnectionState_CONNECTION_STATE_NONE,
	"pending":   socialv1.ConnectionState_CONNECTION_STATE_PENDING,
	"connected": socialv1.ConnectionState_CONNECTION_STATE_CONNECTED,
}

func (h *Handler) ListPeopleMet(ctx context.Context, req *socialv1.ListPeopleMetRequest) (*socialv1.ListPeopleMetResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	people, err := h.svc.ListPeopleMet(ctx, req.GetPlanId(), userID)
	if err != nil {
		return nil, connErr(err, "failed to list people met")
	}
	out := make([]*socialv1.PersonMet, 0, len(people))
	for _, p := range people {
		out = append(out, &socialv1.PersonMet{
			UserId: p.UserID, DisplayName: p.DisplayName, Occupation: p.Occupation,
			PhotoUrl: p.PhotoURL, State: stateToProto[p.State],
		})
	}
	return &socialv1.ListPeopleMetResponse{People: out}, nil
}

func (h *Handler) CreateMeetAgainGroup(ctx context.Context, req *socialv1.CreateMeetAgainGroupRequest) (*socialv1.CreateMeetAgainGroupResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	roomID, err := h.svc.CreateMeetAgainGroup(ctx, req.GetPlanId(), userID, req.GetInviteeIds(), req.GetIdempotencyKey())
	if err != nil {
		return nil, connErr(err, "failed to create group")
	}
	return &socialv1.CreateMeetAgainGroupResponse{RoomId: roomID}, nil
}

func (h *Handler) GetConnectionStatus(ctx context.Context, req *socialv1.GetConnectionStatusRequest) (*socialv1.GetConnectionStatusResponse, error) {
	me, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	state, id, err := h.svc.GetStatus(ctx, me, req.GetUserId())
	if err != nil {
		return nil, connErr(err, "failed to load connection status")
	}
	return &socialv1.GetConnectionStatusResponse{State: state, ConnectionId: id}, nil
}
