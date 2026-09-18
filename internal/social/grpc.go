package social

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.SocialServiceServer. CreatePost/GetPost are
// real; ListPosts/CommentOnPost/LikePost inherit
// socialv1.UnimplementedSocialServiceServer — see PRD §13.10.
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
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create post")
	}
	return toProto(created), nil
}

func (h *Handler) GetPost(ctx context.Context, req *socialv1.GetPostRequest) (*socialv1.Post, error) {
	p, err := h.svc.GetPost(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "post not found")
	}
	return toProto(p), nil
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
