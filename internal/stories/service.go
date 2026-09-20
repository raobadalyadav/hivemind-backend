package stories

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/hivemind/backend/internal/notifications"
	"time"

	"github.com/hivemind/backend/pkg/media"
)

// MaxEditsBytes bounds the overlay/filter JSON a client may attach.
const MaxEditsBytes = 8 << 10

var ErrMediaUnavailable = errors.New("stories: media uploads are not configured")

// ContentScreener is satisfied by *moderation.Screener.
type ContentScreener interface {
	Screen(ctx context.Context, body string) (severity, reason string)
}

type Service struct {
	repo     *Repository
	screener ContentScreener
	media    media.Resolver
	now      func() time.Time
}

// WithMedia enables story creation (uploads are attached by id).
func (s *Service) WithMedia(m media.Resolver) *Service {
	s.media = m
	return s
}

func NewService(repo *Repository, screener ContentScreener) *Service {
	return &Service{repo: repo, screener: screener, now: time.Now}
}

// validEdits accepts only a JSON object of bounded size; the shape inside is
// interpreted by clients, the server just refuses anything that isn't one.
func validEdits(e string) (string, bool) {
	if e == "" {
		return "{}", true
	}
	var v map[string]any
	if len(e) > MaxEditsBytes || json.Unmarshal([]byte(e), &v) != nil {
		return "", false
	}
	return e, true
}

func (s *Service) CreateStory(ctx context.Context, st *Story) (*Story, error) {
	edits, ok := validEdits(st.Edits)
	if st.AuthorID == "" || st.MediaID == "" || len(st.Caption) > 500 || !ok {
		return nil, ErrInvalidInput
	}
	st.Edits = edits
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
	if s.media == nil {
		return nil, ErrMediaUnavailable
	}
	assets, err := s.media.Claim(ctx, st.AuthorID, []string{st.MediaID})
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			return nil, ErrInvalidInput
		}
		return nil, err
	}
	a := assets[0]
	st.MediaURL, st.MediaType, st.ThumbURL, st.Width, st.Height, st.DurationMS = a.URL, a.Kind, a.ThumbURL, a.Width, a.Height, a.DurationMS

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

// MarkViewed records a view of a story the caller may see and, the first time,
// tells the author. Viewing your own story is a no-op.
func (s *Service) MarkViewed(ctx context.Context, storyID, viewerID string) error {
	if storyID == "" || viewerID == "" {
		return ErrInvalidInput
	}
	author, err := s.repo.CanSee(ctx, storyID, viewerID, s.now())
	if err != nil {
		return err
	}
	if author == viewerID {
		return nil
	}
	first, err := s.repo.RecordView(ctx, storyID, viewerID)
	if err != nil || !first {
		return err
	}
	_, _ = notifications.Emit(ctx, s.repo.pool, notifications.Event{
		UserID: author, ActorID: viewerID, Type: notifications.TypeStoryView, TargetID: storyID,
		Title: "{actor} viewed your story", DeepLink: "hivemind://stories/" + author,
		DedupeKey: "storyview:" + storyID + ":" + viewerID,
	})
	return nil
}

// SetLike likes or unlikes a story the caller may see; only a new like notifies the author.
func (s *Service) SetLike(ctx context.Context, storyID, userID string, like bool) (int32, error) {
	if storyID == "" || userID == "" {
		return 0, ErrInvalidInput
	}
	author, err := s.repo.CanSee(ctx, storyID, userID, s.now())
	if err != nil {
		return 0, err
	}
	count, inserted, err := s.repo.SetLike(ctx, storyID, userID, like)
	if err != nil {
		return 0, err
	}
	if inserted && author != userID {
		_, _ = notifications.Emit(ctx, s.repo.pool, notifications.Event{
			UserID: author, ActorID: userID, Type: notifications.TypeStoryLike, TargetID: storyID,
			Title: "{actor} liked your story", DeepLink: "hivemind://stories/" + author,
			DedupeKey: "storylike:" + storyID + ":" + userID,
		})
	}
	return count, nil
}

func (s *Service) ListViewers(ctx context.Context, storyID, userID string) ([]*Viewer, error) {
	if storyID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	own, err := s.repo.IsAuthor(ctx, storyID, userID)
	if err != nil {
		return nil, err
	}
	if !own {
		return nil, ErrForbidden
	}
	return s.repo.ListViewers(ctx, storyID, 200)
}
