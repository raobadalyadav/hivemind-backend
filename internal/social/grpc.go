package social

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

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

func socialErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrContentRejected:
		return status.Error(codes.FailedPrecondition, err.Error())
	case ErrMediaUnavailable:
		return status.Error(codes.Unimplemented, err.Error())
	case ErrNotAttendee, ErrNotMember:
		return status.Error(codes.PermissionDenied, err.Error())
	case ErrPostNotFound:
		// A hidden post and a missing one look identical to the caller.
		return status.Error(codes.NotFound, "post not found")
	}
	return status.Error(codes.Internal, fallback)
}

func (h *Handler) CreatePost(ctx context.Context, req *socialv1.CreatePostRequest) (*socialv1.Post, error) {
	authorID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p := &Post{
		AuthorID:    authorID,
		PlanID:      req.GetPlanId(),
		CommunityID: req.GetCommunityId(),
		Body:        req.GetBody(),
		Visibility:  req.GetVisibility(),
	}
	for _, m := range req.GetMedia() {
		p.Media = append(p.Media, Media{ID: m.GetMediaId()})
	}
	created, err := h.svc.CreatePost(ctx, p)
	if err != nil {
		return nil, socialErr(err, "failed to create post")
	}
	return toProto(created), nil
}

func (h *Handler) GetPost(ctx context.Context, req *socialv1.GetPostRequest) (*socialv1.Post, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p, err := h.svc.GetPost(ctx, req.GetId(), callerID)
	if err != nil {
		return nil, socialErr(err, "failed to get post")
	}
	return toProto(p), nil
}

func postsToProto(list []*Post) []*socialv1.Post {
	out := make([]*socialv1.Post, 0, len(list))
	for _, p := range list {
		out = append(out, toProto(p))
	}
	return out
}

func (h *Handler) ListPosts(ctx context.Context, req *socialv1.ListPostsRequest) (*socialv1.ListPostsResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListPosts(ctx, req.GetAuthorId(), callerID)
	if err != nil {
		return nil, socialErr(err, "failed to list posts")
	}
	return &socialv1.ListPostsResponse{Posts: postsToProto(list)}, nil
}

func (h *Handler) CommentOnPost(ctx context.Context, req *socialv1.CommentOnPostRequest) (*socialv1.Comment, error) {
	authorID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	c, err := h.svc.CommentOnPost(ctx, &Comment{PostID: req.GetPostId(), AuthorID: authorID, Body: req.GetBody()})
	if err != nil {
		return nil, socialErr(err, "failed to comment on post")
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
		return nil, socialErr(err, "failed to like post")
	}
	return &socialv1.LikePostResponse{LikeCount: count}, nil
}

var scopeToDB = map[socialv1.FeedScope]string{
	socialv1.FeedScope_FEED_SCOPE_GLOBAL:      "global",
	socialv1.FeedScope_FEED_SCOPE_CONNECTIONS: "connections",
	socialv1.FeedScope_FEED_SCOPE_COMMUNITY:   "community",
}

func (h *Handler) GetFeed(ctx context.Context, req *socialv1.GetFeedRequest) (*socialv1.GetFeedResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	posts, next, err := h.svc.GetFeed(ctx, userID, scopeToDB[req.GetScope()], req.GetCommunityId(),
		req.GetPage().GetPageSize(), req.GetPage().GetPageToken())
	if err != nil {
		return nil, socialErr(err, "failed to load feed")
	}
	return &socialv1.GetFeedResponse{Posts: postsToProto(posts),
		Page: &socialv1.PageResponse{NextPageToken: next, HasMore: next != ""}}, nil
}

func (h *Handler) SavePost(ctx context.Context, req *socialv1.SavePostRequest) (*socialv1.SavePostResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.SavePost(ctx, req.GetPostId(), userID); err != nil {
		return nil, socialErr(err, "failed to save post")
	}
	return &socialv1.SavePostResponse{}, nil
}

func (h *Handler) UnsavePost(ctx context.Context, req *socialv1.UnsavePostRequest) (*socialv1.UnsavePostResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.UnsavePost(ctx, req.GetPostId(), userID); err != nil {
		return nil, socialErr(err, "failed to unsave post")
	}
	return &socialv1.UnsavePostResponse{}, nil
}

func (h *Handler) ListSavedPosts(ctx context.Context, _ *socialv1.ListSavedPostsRequest) (*socialv1.ListSavedPostsResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListSavedPosts(ctx, userID)
	if err != nil {
		return nil, socialErr(err, "failed to list saved posts")
	}
	return &socialv1.ListSavedPostsResponse{Posts: postsToProto(list)}, nil
}

func (h *Handler) SharePost(ctx context.Context, req *socialv1.SharePostRequest) (*socialv1.SharePostResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	link, err := h.svc.SharePost(ctx, req.GetPostId(), userID)
	if err != nil {
		return nil, socialErr(err, "failed to share post")
	}
	return &socialv1.SharePostResponse{Link: link}, nil
}

func toProto(p *Post) *socialv1.Post {
	out := &socialv1.Post{
		Id:           p.ID,
		AuthorId:     p.AuthorID,
		PlanId:       p.PlanID,
		Body:         p.Body,
		MediaUrls:    p.MediaURLs,
		Visibility:   p.Visibility,
		Audit:        &socialv1.Audit{CreatedAt: timestamppb.New(p.CreatedAt)},
		CommunityId:  p.CommunityID,
		LikeCount:    p.LikeCount,
		CommentCount: p.CommentCount,
		LikedByMe:    p.LikedByMe,
		SavedByMe:    p.SavedByMe,
	}
	for _, m := range p.Media {
		out.Media = append(out.Media, &socialv1.PostMedia{Url: m.URL, MediaType: m.Type, MediaId: m.ID, ThumbUrl: m.ThumbURL, Width: m.Width, Height: m.Height, DurationMs: m.DurationMS})
	}
	return out
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
