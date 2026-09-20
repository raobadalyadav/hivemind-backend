package stories

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

type Handler struct {
	socialv1.UnimplementedStoryServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func storyErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrContentRejected:
		return status.Error(codes.FailedPrecondition, err.Error())
	case ErrNotMember, ErrForbidden:
		return status.Error(codes.PermissionDenied, err.Error())
	case ErrNotFound:
		return status.Error(codes.NotFound, err.Error())
	case ErrMediaUnavailable:
		return status.Error(codes.Unimplemented, err.Error())
	}
	return status.Error(codes.Internal, fallback)
}

var audienceToDB = map[socialv1.StoryAudience]string{
	socialv1.StoryAudience_STORY_AUDIENCE_CONNECTIONS: "connections",
	socialv1.StoryAudience_STORY_AUDIENCE_COMMUNITY:   "community",
}

func toProto(s *Story, caller string) *socialv1.Story {
	aud := socialv1.StoryAudience_STORY_AUDIENCE_CONNECTIONS
	if s.Audience == "community" {
		aud = socialv1.StoryAudience_STORY_AUDIENCE_COMMUNITY
	}
	edits := s.Edits
	if edits == "{}" {
		edits = ""
	}
	return &socialv1.Story{
		Id: s.ID, AuthorId: s.AuthorID, MediaUrl: s.MediaURL, MediaType: s.MediaType, Caption: s.Caption,
		Audience: aud, CommunityId: s.CommunityID, KeepArchive: s.KeepArchive,
		CreatedAt: timestamppb.New(s.CreatedAt), ExpiresAt: timestamppb.New(s.ExpiresAt),
		Media: &socialv1.MediaAsset{Id: s.MediaID, Url: s.MediaURL, ThumbUrl: s.ThumbURL, Kind: s.MediaType,
			Width: s.Width, Height: s.Height, DurationMs: s.DurationMS},
		EditsJson: edits, LikeCount: s.LikeCount, LikedByMe: s.LikedByMe,
		ViewerCount: viewerCountFor(s, caller),
	}
}

func (h *Handler) CreateStory(ctx context.Context, req *socialv1.CreateStoryRequest) (*socialv1.Story, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	st, err := h.svc.CreateStory(ctx, &Story{
		AuthorID: userID, MediaID: req.GetMediaId(), Edits: req.GetEditsJson(), Caption: req.GetCaption(),
		Audience: audienceToDB[req.GetAudience()], CommunityID: req.GetCommunityId(), KeepArchive: req.GetKeepArchive(),
	})
	if err != nil {
		return nil, storyErr(err, "failed to create story")
	}
	return toProto(st, userID), nil
}

func (h *Handler) ListStories(ctx context.Context, _ *socialv1.ListStoriesRequest) (*socialv1.ListStoriesResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	groups, err := h.svc.ListStories(ctx, userID)
	if err != nil {
		return nil, storyErr(err, "failed to list stories")
	}
	out := &socialv1.ListStoriesResponse{}
	for _, g := range groups {
		pg := &socialv1.StoryGroup{AuthorId: g.AuthorID, AuthorName: g.AuthorName}
		for _, s := range g.Stories {
			pg.Stories = append(pg.Stories, toProto(s, userID))
		}
		out.Groups = append(out.Groups, pg)
	}
	return out, nil
}

func (h *Handler) ListMyStories(ctx context.Context, req *socialv1.ListMyStoriesRequest) (*socialv1.ListMyStoriesResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListMyStories(ctx, userID, req.GetIncludeExpired())
	if err != nil {
		return nil, storyErr(err, "failed to list stories")
	}
	out := &socialv1.ListMyStoriesResponse{}
	for _, s := range list {
		out.Stories = append(out.Stories, toProto(s, userID))
	}
	return out, nil
}

func (h *Handler) DeleteStory(ctx context.Context, req *socialv1.DeleteStoryRequest) (*socialv1.DeleteStoryResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.DeleteStory(ctx, req.GetStoryId(), userID); err != nil {
		return nil, storyErr(err, "failed to delete story")
	}
	return &socialv1.DeleteStoryResponse{}, nil
}

// viewerCountFor: only the author learns how many people viewed a story.
func viewerCountFor(s *Story, caller string) int32 {
	if s.AuthorID == caller {
		return s.ViewerCount
	}
	return 0
}

func (h *Handler) MarkStoryViewed(ctx context.Context, req *socialv1.MarkStoryViewedRequest) (*socialv1.MarkStoryViewedResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.MarkViewed(ctx, req.GetStoryId(), userID); err != nil {
		return nil, storyErr(err, "failed to record view")
	}
	return &socialv1.MarkStoryViewedResponse{}, nil
}

func (h *Handler) setLike(ctx context.Context, storyID string, like bool) (*socialv1.StoryLikeResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	n, err := h.svc.SetLike(ctx, storyID, userID, like)
	if err != nil {
		return nil, storyErr(err, "failed to update like")
	}
	return &socialv1.StoryLikeResponse{LikeCount: n, Liked: like}, nil
}

func (h *Handler) LikeStory(ctx context.Context, req *socialv1.StoryLikeRequest) (*socialv1.StoryLikeResponse, error) {
	return h.setLike(ctx, req.GetStoryId(), true)
}

func (h *Handler) UnlikeStory(ctx context.Context, req *socialv1.StoryLikeRequest) (*socialv1.StoryLikeResponse, error) {
	return h.setLike(ctx, req.GetStoryId(), false)
}

func (h *Handler) ListStoryViewers(ctx context.Context, req *socialv1.ListStoryViewersRequest) (*socialv1.ListStoryViewersResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListViewers(ctx, req.GetStoryId(), userID)
	if err != nil {
		return nil, storyErr(err, "failed to list viewers")
	}
	out := make([]*socialv1.StoryViewer, 0, len(list))
	for _, v := range list {
		out = append(out, &socialv1.StoryViewer{UserId: v.UserID, DisplayName: v.DisplayName, PhotoUrl: v.PhotoURL, ViewedAt: timestamppb.New(v.ViewedAt), Liked: v.Liked})
	}
	return &socialv1.ListStoryViewersResponse{Viewers: out}, nil
}
