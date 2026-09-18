package social

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput = errors.New("social: invalid input")
	ErrForbidden    = errors.New("social: this post is private")
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreatePost(ctx context.Context, p *Post) (*Post, error) {
	if p.AuthorID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, p)
}

// checkVisible is the one place private-post access control lives — GetPost,
// CommentOnPost, and LikePost all route through it. Without this, a
// private post's UUID (leaked via a share link, a log line, anywhere) gave
// full read/comment/like access to anyone, since only ListPosts' query
// filtered on visibility — direct-by-ID access bypassed it entirely.
func (s *Service) checkVisible(p *Post, callerID string) error {
	if p.Visibility == "private" && p.AuthorID != callerID {
		return ErrForbidden
	}
	return nil
}

func (s *Service) GetPost(ctx context.Context, id, callerID string) (*Post, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.checkVisible(p, callerID); err != nil {
		return nil, err
	}
	return p, nil
}

const defaultPostPageSize = 20

func (s *Service) ListPosts(ctx context.Context, authorID, callerID string) ([]*Post, error) {
	if authorID == "" {
		return nil, ErrInvalidInput
	}
	publicOnly := authorID != callerID
	return s.repo.ListForAuthor(ctx, authorID, publicOnly, defaultPostPageSize)
}

func (s *Service) CommentOnPost(ctx context.Context, c *Comment) (*Comment, error) {
	if c.PostID == "" || c.AuthorID == "" || c.Body == "" {
		return nil, ErrInvalidInput
	}
	post, err := s.repo.Get(ctx, c.PostID)
	if err != nil {
		return nil, err
	}
	if err := s.checkVisible(post, c.AuthorID); err != nil {
		return nil, err
	}
	return s.repo.CreateComment(ctx, c)
}

func (s *Service) LikePost(ctx context.Context, postID, userID string) (int32, error) {
	if postID == "" || userID == "" {
		return 0, ErrInvalidInput
	}
	post, err := s.repo.Get(ctx, postID)
	if err != nil {
		return 0, err
	}
	if err := s.checkVisible(post, userID); err != nil {
		return 0, err
	}
	return s.repo.Like(ctx, postID, userID)
}
