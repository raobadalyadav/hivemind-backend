package chat

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.ChatServiceServer — every RPC is fully
// implemented (see PRD §13.7).
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
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrNotAMember:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to send message")
		}
	}
	return toProtoMessage(sent), nil
}

func (h *Handler) ListMessages(ctx context.Context, req *socialv1.ListMessagesRequest) (*socialv1.ListMessagesResponse, error) {
	list, err := h.svc.ListMessages(ctx, req.GetRoomId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to list messages")
	}
	out := make([]*socialv1.Message, 0, len(list))
	for _, m := range list {
		out = append(out, toProtoMessage(m))
	}
	return &socialv1.ListMessagesResponse{Messages: out}, nil
}

func (h *Handler) ReportMessage(ctx context.Context, req *socialv1.ReportMessageRequest) (*socialv1.ReportMessageResponse, error) {
	caseID, err := h.svc.ReportMessage(ctx, req.GetMessageId(), req.GetReporterId(), req.GetReason())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrMessageNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to report message")
		}
	}
	return &socialv1.ReportMessageResponse{ModerationCaseId: caseID}, nil
}

func toProtoMessage(m *Message) *socialv1.Message {
	return &socialv1.Message{
		Id:       m.ID,
		RoomId:   m.RoomID,
		SenderId: m.SenderID,
		Body:     m.Body,
		SentAt:   timestamppb.New(m.SentAt),
	}
}
