package social

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/hivemind/backend/internal/notifications"
	"strconv"
	"strings"
	"time"

	"github.com/hivemind/backend/pkg/media"
)

var (
	ErrInvalidInput     = errors.New("social: invalid input")
	ErrContentRejected  = errors.New("social: post violates content policy")
	ErrNotAttendee      = errors.New("social: you can only tag plans you attended or host")
	ErrNotMember        = errors.New("social: you must be a member of that community")
	ErrMediaUnavailable = errors.New("social: media uploads are not configured")
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
	media    media.Resolver
	webBase  string // https origin for shareable links
}

// WithMedia enables posts with photos/videos (attached by upload id).
// WithWebBaseURL sets the https origin used in shared links (no trailing slash).
func (s *Service) WithWebBaseURL(u string) *Service {
	if u != "" {
		s.webBase = u
	}
	return s
}

func (s *Service) WithMedia(m media.Resolver) *Service {
	s.media = m
	return s
}

func NewService(repo *Repository, reporter ReportSubmitter, screener ContentScreener) *Service {
	return &Service{repo: repo, reporter: reporter, screener: screener, webBase: "https://hivemind.app"}
}

const maxForYouOffset = 200

const (
	maxPostBody   = 5000
	maxCommentLen = 2000
	maxPostMedia  = 10
)

var validVisibility = map[string]bool{"private": true, "public": true, "connections": true, "community": true}

func (s *Service) CreatePost(ctx context.Context, p *Post) (*Post, error) {
	if p.AuthorID == "" || len(p.Body) > maxPostBody {
		return nil, ErrInvalidInput
	}
	if p.Visibility == "" {
		p.Visibility = "private"
	}
	if !validVisibility[p.Visibility] || (p.Visibility == "community") != (p.CommunityID != "") {
		return nil, ErrInvalidInput
	}

	// Media is attached by upload id; each must be the author's own upload.
	// (Legacy link fields are ignored — links can't be posted any more.)
	p.MediaURLs = nil
	if len(p.Media) > maxPostMedia || (strings.TrimSpace(p.Body) == "" && len(p.Media) == 0) {
		return nil, ErrInvalidInput
	}
	if len(p.Media) > 0 {
		if s.media == nil {
			return nil, ErrMediaUnavailable
		}
		ids := make([]string, len(p.Media))
		for i, m := range p.Media {
			ids[i] = m.ID
		}
		assets, err := s.media.Claim(ctx, p.AuthorID, ids)
		if err != nil {
			if errors.Is(err, media.ErrNotFound) {
				return nil, ErrInvalidInput
			}
			return nil, err
		}
		p.Media = make([]Media, len(assets))
		for i, a := range assets {
			p.Media[i] = Media{ID: a.ID, URL: a.URL, Type: a.Kind, ThumbURL: a.ThumbURL, Width: a.Width, Height: a.Height, DurationMS: a.DurationMS}
		}
	}

	if p.CommunityID != "" {
		ok, err := s.repo.IsCommunityMember(ctx, p.CommunityID, p.AuthorID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNotMember
		}
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

// GetPost, Comment, Like, Save and Share all resolve the post through
// repo.GetVisible, so they share exactly the feed's visibility rule.
func (s *Service) GetPost(ctx context.Context, id, callerID string) (*Post, error) {
	if id == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.GetVisible(ctx, id, callerID)
}

const maxCommentsPerPost = 200

// ListComments: same visibility rule as GetPost (a hidden post is NotFound).
// ponytail: newest 200 in one page; cursor paging when threads get that long.
func (s *Service) ListComments(ctx context.Context, postID, callerID string) ([]*Comment, error) {
	if postID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	if _, err := s.repo.GetVisible(ctx, postID, callerID); err != nil {
		return nil, err
	}
	return s.repo.ListComments(ctx, postID, callerID, maxCommentsPerPost)
}

const defaultPostPageSize = 20

// ListPosts pages an author's posts newest-first (what callerID may see: own profile = everything,
// someone else's = public / connections-if-connected / community-if-member, never across a block).
func (s *Service) ListPosts(ctx context.Context, authorID, callerID string, pageSize int32, pageToken string) ([]*Post, string, error) {
	if authorID == "" || callerID == "" {
		return nil, "", ErrInvalidInput
	}
	cur, err := decodeCursor(pageToken)
	if err != nil {
		return nil, "", err
	}
	limit := int(pageSize)
	if limit <= 0 || limit > 50 {
		limit = defaultPostPageSize
	}
	posts, err := s.repo.ListForAuthor(ctx, authorID, callerID, cur, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(posts) > limit {
		posts = posts[:limit]
		next = encodeCursor(posts[limit-1])
	}
	return posts, next, nil
}

func (s *Service) CommentOnPost(ctx context.Context, c *Comment) (*Comment, error) {
	if c.PostID == "" || c.AuthorID == "" || strings.TrimSpace(c.Body) == "" || len(c.Body) > maxCommentLen {
		return nil, ErrInvalidInput
	}
	post, err := s.repo.GetVisible(ctx, c.PostID, c.AuthorID)
	if err != nil {
		return nil, err
	}
	severity, reason := s.screener.Screen(ctx, c.Body)
	if severity == "severe" {
		return nil, ErrContentRejected
	}
	created, err := s.repo.CreateComment(ctx, c)
	if err != nil {
		return nil, err
	}
	if severity == "review" {
		_, _ = s.reporter.AutoFlagForSubject(ctx, "comment", created.ID, c.AuthorID, severity, reason)
	}
	s.repo.notify(ctx, notifications.Event{
		UserID: post.AuthorID, ActorID: c.AuthorID, Type: notifications.TypePostComment, TargetID: post.ID,
		Title: "{actor} commented on your post", Body: snippet(c.Body, 80), DeepLink: "hivemind://posts/" + post.ID,
		DedupeKey: "comment:" + created.ID,
	})
	return created, nil
}

func (s *Service) LikePost(ctx context.Context, postID, userID string) (int32, error) {
	if postID == "" || userID == "" {
		return 0, ErrInvalidInput
	}
	post, err := s.repo.GetVisible(ctx, postID, userID)
	if err != nil {
		return 0, err
	}
	count, inserted, err := s.repo.Like(ctx, postID, userID)
	if err != nil {
		return 0, err
	}
	if inserted {
		s.repo.notify(ctx, notifications.Event{
			UserID: post.AuthorID, ActorID: userID, Type: notifications.TypePostLike, TargetID: post.ID,
			Title: "{actor} liked your post", Body: snippet(post.Body, 80), DeepLink: "hivemind://posts/" + post.ID,
			DedupeKey: "like:" + post.ID + ":" + userID,
		})
	}
	return count, nil
}

func (s *Service) SavePost(ctx context.Context, postID, userID string) error {
	if postID == "" || userID == "" {
		return ErrInvalidInput
	}
	if _, err := s.repo.GetVisible(ctx, postID, userID); err != nil {
		return err
	}
	return s.repo.Save(ctx, postID, userID)
}

func (s *Service) UnsavePost(ctx context.Context, postID, userID string) error {
	if postID == "" || userID == "" {
		return ErrInvalidInput
	}
	return s.repo.Unsave(ctx, postID, userID)
}

func (s *Service) ListSavedPosts(ctx context.Context, userID string) ([]*Post, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListSaved(ctx, userID, 50) // ponytail: no paging until saved lists get long
}

func (s *Service) SharePost(ctx context.Context, postID, userID string) (string, error) {
	if postID == "" || userID == "" {
		return "", ErrInvalidInput
	}
	if _, err := s.repo.GetVisible(ctx, postID, userID); err != nil {
		return "", err
	}
	return s.webBase + "/posts/" + postID, nil
}

type cursor struct {
	At time.Time
	ID string
}

func encodeCursor(p *Post) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(p.CreatedAt.UnixMicro(), 10) + "|" + p.ID))
}

func decodeCursor(tok string) (*cursor, error) {
	if tok == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return nil, ErrInvalidInput
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	micros, perr := strconv.ParseInt(ts, 10, 64)
	if !ok || perr != nil || id == "" {
		return nil, ErrInvalidInput
	}
	return &cursor{At: time.UnixMicro(micros).UTC(), ID: id}, nil
}

// GetFeed returns one page plus the next-page token ("" at the end). The
// community scope requires membership — a non-member gets the same empty-ish
// NotFound as a missing community, not an empty feed that confirms it exists.
func (s *Service) GetFeed(ctx context.Context, callerID, scope, communityID string, pageSize int32, pageToken string) ([]*Post, string, error) {
	if callerID == "" || (scope != "global" && scope != "connections" && scope != "community" && scope != "for_you") {
		return nil, "", ErrInvalidInput
	}
	if scope == "for_you" {
		offset := 0
		if pageToken != "" {
			n, err := strconv.Atoi(pageToken)
			if err != nil || n < 0 || n > maxForYouOffset {
				return nil, "", ErrInvalidInput
			}
			offset = n
		}
		limit := int(pageSize)
		if limit <= 0 || limit > 50 {
			limit = defaultPostPageSize
		}
		posts, err := s.repo.ForYou(ctx, callerID, offset, limit+1)
		if err != nil {
			return nil, "", err
		}
		next := ""
		if len(posts) > limit {
			posts = posts[:limit]
			if offset+limit <= maxForYouOffset {
				next = strconv.Itoa(offset + limit)
			}
		}
		return posts, next, nil
	}
	if (scope == "community") != (communityID != "") {
		return nil, "", ErrInvalidInput
	}
	if scope == "community" {
		ok, err := s.repo.IsCommunityMember(ctx, communityID, callerID)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, "", ErrPostNotFound
		}
	}
	cur, err := decodeCursor(pageToken)
	if err != nil {
		return nil, "", err
	}
	limit := int(pageSize)
	if limit <= 0 || limit > 50 {
		limit = defaultPostPageSize
	}
	posts, err := s.repo.Feed(ctx, callerID, scope, communityID, cur, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(posts) > limit {
		posts = posts[:limit]
		next = encodeCursor(posts[limit-1])
	}
	return posts, next, nil
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

// snippet is the first n runes of s, for notification bodies.
func snippet(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
