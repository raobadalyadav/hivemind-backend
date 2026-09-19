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
	}
	return status.Error(codes.Internal, fallback)
}

var audienceToDB = map[socialv1.StoryAudience]string{
	socialv1.StoryAudience_STORY_AUDIENCE_CONNECTIONS: "connections",
	socialv1.StoryAudience_STORY_AUDIENCE_COMMUNITY:   "community",
}

func toProto(s *Story) *socialv1.Story {
	aud := socialv1.StoryAudience_STORY_AUDIENCE_CONNECTIONS
	if s.Audience == "community" {
		aud = socialv1.StoryAudience_STORY_AUDIENCE_COMMUNITY
	}
	return &socialv1.Story{
		Id: s.ID, AuthorId: s.AuthorID, MediaUrl: s.MediaURL, MediaType: s.MediaType, Caption: s.Caption,
		Audience: aud, CommunityId: s.CommunityID, KeepArchive: s.KeepArchive,
		CreatedAt: timestamppb.New(s.CreatedAt), ExpiresAt: timestamppb.New(s.ExpiresAt),
	}
}

func (h *Handler) CreateStory(ctx context.Context, req *socialv1.CreateStoryRequest) (*socialv1.Story, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	st, err := h.svc.CreateStory(ctx, &Story{
		AuthorID: userID, MediaURL: req.GetMediaUrl(), MediaType: req.GetMediaType(), Caption: req.GetCaption(),
		Audience: audienceToDB[req.GetAudience()], CommunityID: req.GetCommunityId(), KeepArchive: req.GetKeepArchive(),
	})
	if err != nil {
		return nil, storyErr(err, "failed to create story")
	}
	return toProto(st), nil
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
			pg.Stories = append(pg.Stories, toProto(s))
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
		out.Stories = append(out.Stories, toProto(s))
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
