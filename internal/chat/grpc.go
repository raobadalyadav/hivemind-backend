package chat

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.ChatServiceServer. CreateRoom/SendMessage are
// real; ListMessages/ReportMessage inherit
// socialv1.UnimplementedChatServiceServer — see PRD §13.7.
type Handler struct {
	socialv1.UnimplementedChatServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateRoom(ctx context.Context, req *socialv1.CreateRoomRequest) (*socialv1.ChatRoom, error) {
	room, err := h.svc.CreateRoom(ctx, req.GetPlanId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create room")
	}
	return &socialv1.ChatRoom{Id: room.ID, PlanId: room.PlanID}, nil
}

func (h *Handler) SendMessage(ctx context.Context, req *socialv1.SendMessageRequest) (*socialv1.Message, error) {
	m := &Message{RoomID: req.GetRoomId(), SenderID: req.GetSenderId(), Body: req.GetBody()}
	sent, err := h.svc.SendMessage(ctx, m)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to send message")
	}
	return &socialv1.Message{
		Id:       sent.ID,
		RoomId:   sent.RoomID,
		SenderId: sent.SenderID,
		Body:     sent.Body,
		SentAt:   timestamppb.New(sent.SentAt),
	}, nil
}
