package notifications

import (
	"context"
	"google.golang.org/protobuf/types/known/timestamppb"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.NotificationServiceServer — every RPC is fully
// implemented (see PRD §13.15).
type Handler struct {
	socialv1.UnimplementedNotificationServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// SendNotification is not self-referential — the caller sends *to* another
// user, so context-derived identity doesn't apply here. It's gated to
// admin/system callers via pkg/grpcmiddleware's adminMethods instead,
// otherwise any authenticated user could spam arbitrary recipients.
func (h *Handler) SendNotification(ctx context.Context, req *socialv1.SendNotificationRequest) (*socialv1.SendNotificationResponse, error) {
	n := &Notification{
		UserID:   req.GetUserId(),
		Channel:  req.GetChannel(),
		Title:    req.GetTitle(),
		Body:     req.GetBody(),
		DeepLink: req.GetDeepLink(),
	}
	created, err := h.svc.SendNotification(ctx, n)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to send notification")
	}
	return &socialv1.SendNotificationResponse{NotificationId: created.ID}, nil
}

func (h *Handler) ListNotifications(ctx context.Context, req *socialv1.ListNotificationsRequest) (*socialv1.ListNotificationsResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, next, err := h.svc.ListNotifications(ctx, userID, req.GetPage().GetPageToken())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to list notifications")
	}
	out := make([]*socialv1.Notification, 0, len(list))
	for _, n := range list {
		out = append(out, &socialv1.Notification{
			Id:       n.ID,
			UserId:   n.UserID,
			Channel:  n.Channel,
			Title:    n.Title,
			Body:     n.Body,
			DeepLink: n.DeepLink,
			Read:     n.Read,
			Type:     n.Type, ActorId: n.ActorID, ActorName: n.ActorName, ActorPhotoUrl: n.ActorPhotoURL,
			TargetId: n.TargetID, CreatedAt: timestamppb.New(n.CreatedAt),
		})
	}
	return &socialv1.ListNotificationsResponse{Notifications: out, Page: &socialv1.PageResponse{NextPageToken: next, HasMore: next != ""}}, nil
}

func (h *Handler) UpdatePreferences(ctx context.Context, req *socialv1.UpdatePreferencesRequest) (*socialv1.UpdatePreferencesResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	err := h.svc.UpdatePreferences(ctx, userID, req.GetPushEnabled(), req.GetEmailEnabled(),
		req.GetQuietHoursStart(), req.GetQuietHoursEnd())
	if err == nil && req.Muted != nil {
		err = h.svc.SetMuted(ctx, userID, req.GetMuted().GetItems())
	}
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to update preferences")
	}
	return &socialv1.UpdatePreferencesResponse{}, nil
}

func (h *Handler) GetPreferences(ctx context.Context, _ *socialv1.GetPreferencesRequest) (*socialv1.NotificationPreferences, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	push, email, qs, qe, err := h.svc.GetPreferences(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load preferences")
	}
	muted, err := h.svc.MutedCategories(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load preferences")
	}
	return &socialv1.NotificationPreferences{PushEnabled: push, EmailEnabled: email, QuietHoursStart: qs, QuietHoursEnd: qe, MutedCategories: muted}, nil
}

func (h *Handler) MarkNotificationsRead(ctx context.Context, req *socialv1.MarkNotificationsReadRequest) (*socialv1.MarkNotificationsReadResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	unread, err := h.svc.MarkRead(ctx, userID, req.GetIds(), req.GetAll())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to mark notifications read")
	}
	return &socialv1.MarkNotificationsReadResponse{Unread: int32(unread)}, nil
}

func (h *Handler) GetUnreadCount(ctx context.Context, _ *socialv1.GetUnreadCountRequest) (*socialv1.GetUnreadCountResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	n, err := h.svc.UnreadCount(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count notifications")
	}
	return &socialv1.GetUnreadCountResponse{Unread: int32(n)}, nil
}
