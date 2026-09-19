package social

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.SocialServiceServer — every RPC is fully
// implemented (see PRD §13.10).
type Handler struct {
	socialv1.UnimplementedSocialServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreatePost(ctx context.Context, req *socialv1.CreatePostRequest) (*socialv1.Post, error) {
	authorID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p := &Post{
		AuthorID:   authorID,
		PlanID:     req.GetPlanId(),
		Body:       req.GetBody(),
		MediaURLs:  req.GetMediaUrls(),
		Visibility: req.GetVisibility(),
	}
	created, err := h.svc.CreatePost(ctx, p)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrContentRejected:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case ErrNotAttendee:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to create post")
		}
	}
	return toProto(created), nil
}

func (h *Handler) GetPost(ctx context.Context, req *socialv1.GetPostRequest) (*socialv1.Post, error) {
	callerID, _ := grpcmiddleware.UserIDFromContext(ctx)
	// A private post and a nonexistent one both come back as NotFound —
	// deliberately the same code, so a non-author can't distinguish
	// "doesn't exist" from "exists but is private" (see ErrForbidden's
	// handling in checkVisible).
	p, err := h.svc.GetPost(ctx, req.GetId(), callerID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "post not found")
	}
	return toProto(p), nil
}

func (h *Handler) ListPosts(ctx context.Context, req *socialv1.ListPostsRequest) (*socialv1.ListPostsResponse, error) {
	callerID, _ := grpcmiddleware.UserIDFromContext(ctx)
	list, err := h.svc.ListPosts(ctx, req.GetAuthorId(), callerID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to list posts")
	}
	out := make([]*socialv1.Post, 0, len(list))
	for _, p := range list {
		out = append(out, toProto(p))
	}
	return &socialv1.ListPostsResponse{Posts: out}, nil
}

func (h *Handler) CommentOnPost(ctx context.Context, req *socialv1.CommentOnPostRequest) (*socialv1.Comment, error) {
	authorID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	c, err := h.svc.CommentOnPost(ctx, &Comment{PostID: req.GetPostId(), AuthorID: authorID, Body: req.GetBody()})
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.NotFound, "post not found")
		default:
			return nil, status.Error(codes.Internal, "failed to comment on post")
		}
	}
	return &socialv1.Comment{Id: c.ID, PostId: c.PostID, AuthorId: c.AuthorID, Body: c.Body}, nil
}

func (h *Handler) LikePost(ctx context.Context, req *socialv1.LikePostRequest) (*socialv1.LikePostResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	count, err := h.svc.LikePost(ctx, req.GetPostId(), userID)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.NotFound, "post not found")
		default:
			return nil, status.Error(codes.Internal, "failed to like post")
		}
	}
	return &socialv1.LikePostResponse{LikeCount: count}, nil
}

func toProto(p *Post) *socialv1.Post {
	return &socialv1.Post{
		Id:         p.ID,
		AuthorId:   p.AuthorID,
		PlanId:     p.PlanID,
		Body:       p.Body,
		MediaUrls:  p.MediaURLs,
		Visibility: p.Visibility,
	}
}

func (h *Handler) ListMyMemories(ctx context.Context, req *socialv1.ListMyMemoriesRequest) (*socialv1.ListMyMemoriesResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	years, err := h.svc.ListMyMemories(ctx, userID, req.GetYear())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to load memories")
	}
	out := make([]*socialv1.MemoryYear, 0, len(years))
	for _, y := range years {
		my := &socialv1.MemoryYear{Year: y.Year}
		for _, m := range y.Memories {
			my.Memories = append(my.Memories, &socialv1.Memory{
				PlanId: m.PlanID, Title: m.Title, StartsAt: m.StartsAt.UTC().Format(time.RFC3339),
				CityName: m.CityName, PhotoCount: m.PhotoCount, PeopleCount: m.PeopleCount,
			})
		}
		out = append(out, my)
	}
	return &socialv1.ListMyMemoriesResponse{Years: out}, nil
}
