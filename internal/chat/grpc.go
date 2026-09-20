package chat

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
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

var msgTypeToDB = map[socialv1.MessageType]string{
	socialv1.MessageType_MESSAGE_TYPE_TEXT:         "text",
	socialv1.MessageType_MESSAGE_TYPE_IMAGE:        "image",
	socialv1.MessageType_MESSAGE_TYPE_VOICE:        "voice",
	socialv1.MessageType_MESSAGE_TYPE_ANNOUNCEMENT: "announcement",
	socialv1.MessageType_MESSAGE_TYPE_LOCATION:     "location",
}

var msgTypeToProto = map[string]socialv1.MessageType{
	"text":         socialv1.MessageType_MESSAGE_TYPE_TEXT,
	"image":        socialv1.MessageType_MESSAGE_TYPE_IMAGE,
	"voice":        socialv1.MessageType_MESSAGE_TYPE_VOICE,
	"poll":         socialv1.MessageType_MESSAGE_TYPE_POLL,
	"announcement": socialv1.MessageType_MESSAGE_TYPE_ANNOUNCEMENT,
	"location":     socialv1.MessageType_MESSAGE_TYPE_LOCATION,
}

func chatErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrNotAMember, ErrNotHost, ErrNotConnected:
		return status.Error(codes.PermissionDenied, err.Error())
	case ErrContentRejected:
		return status.Error(codes.FailedPrecondition, err.Error())
	case ErrMediaUnavailable:
		return status.Error(codes.Unimplemented, err.Error())
	case ErrMessageNotFound, ErrPollNotFound, ErrRoomNotFound:
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, fallback)
}

func (h *Handler) CreateRoom(ctx context.Context, req *socialv1.CreateRoomRequest) (*socialv1.ChatRoom, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	room, err := h.svc.CreateRoom(ctx, req.GetPlanId(), userID, role)
	if err != nil {
		return nil, chatErr(err, "failed to create room")
	}
	return &socialv1.ChatRoom{Id: room.ID, PlanId: room.PlanID, PinnedMessageId: room.PinnedMessageID}, nil
}

func (h *Handler) SendMessage(ctx context.Context, req *socialv1.SendMessageRequest) (*socialv1.Message, error) {
	senderID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	t, known := msgTypeToDB[req.GetType()]
	if !known { // POLL (or an unknown value) can't be sent as a plain message
		return nil, status.Error(codes.InvalidArgument, ErrInvalidInput.Error())
	}
	m := &Message{
		RoomID: req.GetRoomId(), SenderID: senderID, Body: req.GetBody(), Type: t,
		MediaIDs: req.GetMediaIds(), DurationSeconds: req.GetDurationSeconds(),
	}
	if l := req.GetLocation(); l != nil {
		m.Location = &Location{Latitude: l.GetLatitude(), Longitude: l.GetLongitude(), Label: l.GetLabel()}
	}
	sent, err := h.svc.SendMessage(ctx, m, role)
	if err != nil {
		return nil, chatErr(err, "failed to send message")
	}
	return toProtoMessage(sent), nil
}

func (h *Handler) ListMessages(ctx context.Context, req *socialv1.ListMessagesRequest) (*socialv1.ListMessagesResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, pinned, next, err := h.svc.ListMessagesPage(ctx, req.GetRoomId(), userID, req.GetPage().GetPageToken())
	if err != nil {
		return nil, chatErr(err, "failed to list messages")
	}
	out := make([]*socialv1.Message, 0, len(list))
	for _, m := range list {
		out = append(out, toProtoMessage(m))
	}
	resp := &socialv1.ListMessagesResponse{Messages: out, Page: &socialv1.PageResponse{NextPageToken: next, HasMore: next != ""}}
	if pinned != nil {
		resp.PinnedMessage = toProtoMessage(pinned)
	}
	return resp, nil
}

func (h *Handler) CreatePoll(ctx context.Context, req *socialv1.CreatePollRequest) (*socialv1.Message, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	m, err := h.svc.CreatePoll(ctx, req.GetRoomId(), userID, req.GetQuestion(), req.GetOptions())
	if err != nil {
		return nil, chatErr(err, "failed to create poll")
	}
	return toProtoMessage(m), nil
}

func (h *Handler) VotePoll(ctx context.Context, req *socialv1.VotePollRequest) (*socialv1.Poll, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p, err := h.svc.VotePoll(ctx, req.GetPollId(), req.GetOptionId(), userID)
	if err != nil {
		return nil, chatErr(err, "failed to vote")
	}
	return pollToProto(p), nil
}

func (h *Handler) PinMessage(ctx context.Context, req *socialv1.PinMessageRequest) (*socialv1.PinMessageResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	id, err := h.svc.PinMessage(ctx, req.GetRoomId(), req.GetMessageId(), req.GetUnpin(), userID, role)
	if err != nil {
		return nil, chatErr(err, "failed to pin message")
	}
	return &socialv1.PinMessageResponse{PinnedMessageId: id}, nil
}

func (h *Handler) ReportMessage(ctx context.Context, req *socialv1.ReportMessageRequest) (*socialv1.ReportMessageResponse, error) {
	reporterID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	caseID, err := h.svc.ReportMessage(ctx, req.GetMessageId(), reporterID, req.GetReason())
	if err != nil {
		return nil, chatErr(err, "failed to report message")
	}
	return &socialv1.ReportMessageResponse{ModerationCaseId: caseID}, nil
}

func (h *Handler) GenerateIcebreaker(ctx context.Context, req *socialv1.GenerateIcebreakerRequest) (*socialv1.GenerateIcebreakerResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	text, err := h.svc.GenerateIcebreaker(ctx, req.GetRoomId(), userID)
	if err != nil {
		return nil, chatErr(err, "failed to generate icebreaker")
	}
	return &socialv1.GenerateIcebreakerResponse{Text: text}, nil
}

func pollToProto(p *Poll) *socialv1.Poll {
	out := &socialv1.Poll{Id: p.ID, Question: p.Question, MyVoteOptionId: p.MyVoteOptionID, TotalVotes: p.TotalVotes}
	for _, o := range p.Options {
		out.Options = append(out.Options, &socialv1.PollOption{Id: o.ID, Label: o.Label, VoteCount: o.VoteCount})
	}
	return out
}

func toProtoMessage(m *Message) *socialv1.Message {
	out := &socialv1.Message{
		Id:              m.ID,
		RoomId:          m.RoomID,
		SenderId:        m.SenderID,
		Body:            m.Body,
		SentAt:          timestamppb.New(m.SentAt),
		Type:            msgTypeToProto[m.Type],
		MediaUrls:       m.MediaURLs,
		DurationSeconds: m.DurationSeconds,
	}
	for _, a := range m.Media {
		out.Media = append(out.Media, &socialv1.MediaAsset{Id: a.ID, Url: a.URL, ThumbUrl: a.ThumbURL, Kind: a.Kind, Width: a.Width, Height: a.Height, DurationMs: a.DurationMS})
	}
	if m.Location != nil {
		out.Location = &socialv1.Location{Latitude: m.Location.Latitude, Longitude: m.Location.Longitude, Label: m.Location.Label}
	}
	if m.Poll != nil {
		out.Poll = pollToProto(m.Poll)
	}
	return out
}

var kindToProto = map[string]socialv1.ChatKind{"dm": socialv1.ChatKind_CHAT_KIND_DM, "plan": socialv1.ChatKind_CHAT_KIND_PLAN}

func (h *Handler) ListMyChats(ctx context.Context, req *socialv1.ListMyChatsRequest) (*socialv1.ListMyChatsResponse, error) {
	uid, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	tab := map[socialv1.ChatTab]string{socialv1.ChatTab_CHAT_TAB_PRIMARY: "primary", socialv1.ChatTab_CHAT_TAB_GENERAL: "general", socialv1.ChatTab_CHAT_TAB_ALL: "all", socialv1.ChatTab_CHAT_TAB_GROUPS: "groups"}[req.GetTab()]
	res, err := h.svc.InboxFor(ctx, uid, tab)
	if err != nil {
		return nil, chatErr(err, "failed to load chats")
	}
	out := &socialv1.ListMyChatsResponse{PrimaryUnread: res.PrimaryUnread, GeneralUnread: res.GeneralUnread, GroupsUnread: res.GroupsUnread}
	for _, c := range res.Chats {
		pc := &socialv1.ChatSummary{
			RoomId: c.RoomID, Kind: kindToProto[c.Kind], Title: c.Title, OtherUserId: c.OtherUserID, PlanId: c.PlanID,
			AvatarUrl: c.AvatarURL, OtherVerified: c.OtherVerified, LastMessage: c.LastMessage,
			UnreadCount: c.Unread, Muted: c.Muted,
		}
		if c.LastMessageAt != nil {
			pc.LastMessageAt = timestamppb.New(*c.LastMessageAt)
		}
		out.Chats = append(out.Chats, pc)
	}
	return out, nil
}

func (h *Handler) MarkRead(ctx context.Context, req *socialv1.MarkReadRequest) (*socialv1.MarkReadResponse, error) {
	uid, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.MarkRead(ctx, req.GetRoomId(), uid); err != nil {
		return nil, chatErr(err, "failed to mark read")
	}
	return &socialv1.MarkReadResponse{}, nil
}

func (h *Handler) OpenDirectChat(ctx context.Context, req *socialv1.OpenDirectChatRequest) (*socialv1.ChatRoom, error) {
	uid, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	id, err := h.svc.OpenDirectChat(ctx, uid, req.GetUserId())
	if err != nil {
		return nil, chatErr(err, "failed to open chat")
	}
	return &socialv1.ChatRoom{Id: id}, nil
}
