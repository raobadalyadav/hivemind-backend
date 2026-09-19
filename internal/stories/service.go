package stories

import (
	"context"
	"strings"
	"time"
)

// ContentScreener is satisfied by *moderation.Screener.
type ContentScreener interface {
	Screen(ctx context.Context, body string) (severity, reason string)
}

type Service struct {
	repo     *Repository
	screener ContentScreener
	now      func() time.Time
}

func NewService(repo *Repository, screener ContentScreener) *Service {
	return &Service{repo: repo, screener: screener, now: time.Now}
}

func validHTTPS(u string) bool {
	return len(u) > len("https://") && len(u) <= 2048 && strings.HasPrefix(u, "https://")
}

func (s *Service) CreateStory(ctx context.Context, st *Story) (*Story, error) {
	if st.AuthorID == "" || !validHTTPS(st.MediaURL) || len(st.Caption) > 500 {
		return nil, ErrInvalidInput
	}
	if st.MediaType == "" {
		st.MediaType = "image"
	}
	if st.MediaType != "image" && st.MediaType != "video" {
		return nil, ErrInvalidInput
	}
	if st.Audience != "connections" && st.Audience != "community" {
		return nil, ErrInvalidInput
	}
	if (st.Audience == "community") != (st.CommunityID != "") {
		return nil, ErrInvalidInput
	}
	if st.CommunityID != "" {
		ok, err := s.repo.IsCommunityMember(ctx, st.CommunityID, st.AuthorID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNotMember
		}
	}
	// A story is ephemeral and low-friction, so anything screening flags at
	// all is refused rather than queued for review.
	if sev, _ := s.screener.Screen(ctx, st.Caption); sev == "severe" || sev == "review" {
		return nil, ErrContentRejected
	}
	return s.repo.Create(ctx, st, s.now())
}

// Group is one author's stories, oldest first.
type Group struct {
	AuthorID   string
	AuthorName string
	Stories    []*Story
}

func (s *Service) ListStories(ctx context.Context, viewerID string) ([]Group, error) {
	if viewerID == "" {
		return nil, ErrInvalidInput
	}
	list, err := s.repo.ListVisible(ctx, viewerID, s.now())
	if err != nil {
		return nil, err
	}
	var out []Group
	for _, st := range list { // already ordered: own first, then by author, then oldest first
		if len(out) == 0 || out[len(out)-1].AuthorID != st.AuthorID {
			out = append(out, Group{AuthorID: st.AuthorID, AuthorName: st.AuthorName})
		}
		g := &out[len(out)-1]
		g.Stories = append(g.Stories, st)
	}
	return out, nil
}

func (s *Service) ListMyStories(ctx context.Context, userID string, includeExpired bool) ([]*Story, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListMine(ctx, userID, includeExpired, s.now())
}

func (s *Service) DeleteStory(ctx context.Context, storyID, userID string) error {
	if storyID == "" || userID == "" {
		return ErrInvalidInput
	}
	return s.repo.Delete(ctx, storyID, userID)
}

// PurgeExpired is the hourly worker job.
func (s *Service) PurgeExpired(ctx context.Context, now time.Time) (int, error) {
	return s.repo.PurgeExpired(ctx, now)
}
