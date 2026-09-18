package connections

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.ConnectionServiceServer. RequestConnection/
// ListConnections are real; RespondConnection inherits
// socialv1.UnimplementedConnectionServiceServer — see PRD §13.9/§33.
type Handler struct {
	socialv1.UnimplementedConnectionServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RequestConnection(ctx context.Context, req *socialv1.RequestConnectionRequest) (*socialv1.Connection, error) {
	c := &Connection{
		RequesterID:  req.GetRequesterId(),
		RecipientID:  req.GetRecipientId(),
		OriginPlanID: req.GetOriginPlanId(),
	}
	created, err := h.svc.RequestConnection(ctx, c)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to request connection")
	}
	return toProto(created), nil
}

func (h *Handler) ListConnections(ctx context.Context, req *socialv1.ListConnectionsRequest) (*socialv1.ListConnectionsResponse, error) {
	list, err := h.svc.ListConnections(ctx, req.GetUserId())
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
