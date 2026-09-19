package social

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput    = errors.New("social: invalid input")
	ErrForbidden       = errors.New("social: this post is private")
	ErrContentRejected = errors.New("social: post violates content policy")
	ErrNotAttendee     = errors.New("social: you can only tag plans you attended or host")
)

// ReportSubmitter is satisfied by *moderation.Service — duplicated locally
// per the existing no-cross-import convention (see internal/chat's
// identical interface).
type ReportSubmitter interface {
	AutoFlagForSubject(ctx context.Context, subjectType, subjectID, actorID, severity, reason string) (caseID string, err error)
}

// ContentScreener is satisfied by *moderation.Screener — always available
// (never nil, never errors), unlike truly-optional external services.
type ContentScreener interface {
	Screen(ctx context.Context, body string) (severity, reason string)
}

type Service struct {
	repo     *Repository
	reporter ReportSubmitter
	screener ContentScreener
}

func NewService(repo *Repository, reporter ReportSubmitter, screener ContentScreener) *Service {
	return &Service{repo: repo, reporter: reporter, screener: screener}
}

func (s *Service) CreatePost(ctx context.Context, p *Post) (*Post, error) {
	if p.AuthorID == "" {
		return nil, ErrInvalidInput
	}
	if p.PlanID != "" {
		ok, err := s.repo.CanAttachToPlan(ctx, p.PlanID, p.AuthorID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNotAttendee
		}
	}
	severity, reason := s.screener.Screen(ctx, p.Body)
	if severity == "severe" {
		return nil, ErrContentRejected
	}
	created, err := s.repo.Create(ctx, p)
	if err != nil {
		return nil, err
	}
	if severity == "review" {
		_, _ = s.reporter.AutoFlagForSubject(ctx, "post", created.ID, p.AuthorID, severity, reason)
	}
	return created, nil
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

// MemoryYear groups memories by calendar year, newest year first.
type MemoryYear struct {
	Year     int32
	Memories []Memory
}

func (s *Service) ListMyMemories(ctx context.Context, userID string, year int32) ([]MemoryYear, error) {
	if userID == "" || year < 0 {
		return nil, ErrInvalidInput
	}
	ms, err := s.repo.ListMemories(ctx, userID, year)
	if err != nil {
		return nil, err
	}
	var out []MemoryYear
	for _, m := range ms { // already newest-first, so years arrive grouped
		if len(out) == 0 || out[len(out)-1].Year != m.Year {
			out = append(out, MemoryYear{Year: m.Year})
		}
		out[len(out)-1].Memories = append(out[len(out)-1].Memories, m)
	}
	return out, nil
}
