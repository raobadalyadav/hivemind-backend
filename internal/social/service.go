package social

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidInput    = errors.New("social: invalid input")
	ErrContentRejected = errors.New("social: post violates content policy")
	ErrNotAttendee     = errors.New("social: you can only tag plans you attended or host")
	ErrNotMember       = errors.New("social: you must be a member of that community")
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

const (
	maxPostBody   = 5000
	maxCommentLen = 2000
	maxPostMedia  = 10
)

var validVisibility = map[string]bool{"private": true, "public": true, "connections": true, "community": true}

func validHTTPS(u string) bool {
	return len(u) > len("https://") && len(u) <= 2048 && strings.HasPrefix(u, "https://")
}

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

	// media_urls (images) and typed media merge into one ordered list.
	media := make([]Media, 0, len(p.MediaURLs)+len(p.Media))
	for _, u := range p.MediaURLs {
		media = append(media, Media{URL: u, Type: "image"})
	}
	media = append(media, p.Media...)
	if len(media) > maxPostMedia || (strings.TrimSpace(p.Body) == "" && len(media) == 0) {
		return nil, ErrInvalidInput
	}
	for i := range media {
		if media[i].Type == "" {
			media[i].Type = "image"
		}
		if !validHTTPS(media[i].URL) || (media[i].Type != "image" && media[i].Type != "video") {
			return nil, ErrInvalidInput
		}
	}
	p.Media, p.MediaURLs = media, nil

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

const defaultPostPageSize = 20

func (s *Service) ListPosts(ctx context.Context, authorID, callerID string) ([]*Post, error) {
	if authorID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListForAuthor(ctx, authorID, callerID, defaultPostPageSize)
}

func (s *Service) CommentOnPost(ctx context.Context, c *Comment) (*Comment, error) {
	if c.PostID == "" || c.AuthorID == "" || strings.TrimSpace(c.Body) == "" || len(c.Body) > maxCommentLen {
		return nil, ErrInvalidInput
	}
	if _, err := s.repo.GetVisible(ctx, c.PostID, c.AuthorID); err != nil {
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
	return created, nil
}

func (s *Service) LikePost(ctx context.Context, postID, userID string) (int32, error) {
	if postID == "" || userID == "" {
		return 0, ErrInvalidInput
	}
	if _, err := s.repo.GetVisible(ctx, postID, userID); err != nil {
		return 0, err
	}
	return s.repo.Like(ctx, postID, userID)
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
	return "hivemind://posts/" + postID, nil
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
	if callerID == "" || (scope != "global" && scope != "connections" && scope != "community") {
		return nil, "", ErrInvalidInput
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
