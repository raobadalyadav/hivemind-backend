package chat

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hivemind/backend/pkg/media"
)

var (
	ErrInvalidInput     = errors.New("chat: invalid input")
	ErrContentRejected  = errors.New("chat: message violates content policy")
	ErrMediaUnavailable = errors.New("chat: media uploads are not configured")
)

const defaultMessagePageSize = 50

// ReportSubmitter is satisfied by *moderation.Service (wired in
// cmd/api/main.go) — declared here so chat doesn't import moderation.
type ReportSubmitter interface {
	SubmitReportForSubject(ctx context.Context, reporterID, subjectType, subjectID, reason string) (caseID string, err error)
	AutoFlagForSubject(ctx context.Context, subjectType, subjectID, actorID, severity, reason string) (caseID string, err error)
}

// ContentScreener is satisfied by *moderation.Screener — duplicated locally
// per the existing no-cross-import convention. Always available (never
// nil, never errors), unlike the truly-optional EmailSender/PushSender.
type ContentScreener interface {
	Screen(ctx context.Context, body string) (severity, reason string)
}

type Service struct {
	media      media.Resolver
	repo       *Repository
	reporter   ReportSubmitter
	screener   ContentScreener
	icebreaker IcebreakerGenerator
}

// WithMedia enables photo/video messages (attached by upload id).
func (s *Service) WithMedia(m media.Resolver) *Service {
	s.media = m
	return s
}

func NewService(repo *Repository, reporter ReportSubmitter, screener ContentScreener, icebreaker IcebreakerGenerator) *Service {
	return &Service{repo: repo, reporter: reporter, screener: screener, icebreaker: icebreaker}
}

func isAdmin(role string) bool { return role == "admin" || role == "super_admin" }

func (s *Service) requireMember(ctx context.Context, roomID, userID string) error {
	ok, err := s.repo.IsMember(ctx, roomID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotAMember
	}
	return nil
}

// GenerateIcebreaker: members only (it reveals the room's shared interests).
func (s *Service) GenerateIcebreaker(ctx context.Context, roomID, callerID string) (string, error) {
	if roomID == "" || callerID == "" {
		return "", ErrInvalidInput
	}
	if err := s.requireMember(ctx, roomID, callerID); err != nil {
		return "", err
	}
	interests, err := s.repo.MemberInterests(ctx, roomID)
	if err != nil {
		return "", err
	}
	return s.icebreaker.Generate(ctx, interests)
}

// CreateRoom opens (idempotently) the plan's room, but only for the plan's
// host or a confirmed participant — previously any caller could create a
// room for any plan id. Non-admin callers are also added as members, which
// is how a host (who never books) gets into their own room.
func (s *Service) CreateRoom(ctx context.Context, planID, callerID, callerRole string) (*Room, error) {
	if planID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	if !isAdmin(callerRole) {
		ok, err := s.repo.CanUsePlanRoom(ctx, planID, callerID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNotAMember
		}
	}
	room, err := s.repo.GetOrCreateRoomForPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	if !isAdmin(callerRole) {
		if err := s.repo.AddMember(ctx, room.ID, callerID); err != nil {
			return nil, err
		}
	}
	room.PinnedMessageID, _ = s.repo.PinnedMessageID(ctx, room.ID)
	return room, nil
}

// CreateAdHocRoomWithMembers satisfies the RoomCreator interface declared
// locally in internal/availability and internal/smartgroups — the one new
// room-creation path both packages share.
func (s *Service) CreateAdHocRoomWithMembers(ctx context.Context, userIDs []string) (string, error) {
	if len(userIDs) == 0 {
		return "", ErrInvalidInput
	}
	room, err := s.repo.CreateAdHocRoom(ctx)
	if err != nil {
		return "", err
	}
	for _, userID := range userIDs {
		if err := s.repo.AddMember(ctx, room.ID, userID); err != nil {
			return "", err
		}
	}
	return room.ID, nil
}

// EnsureMembership is called by cmd/worker's BOOKING_CONFIRMED handler —
// idempotent room lookup/create plus member add, so retried or duplicate
// events are harmless. This is what makes PRD §31's "confirmed participants
// receive the room" actually true.
func (s *Service) EnsureMembership(ctx context.Context, planID, userID string) error {
	if planID == "" || userID == "" {
		return ErrInvalidInput
	}
	room, err := s.repo.GetOrCreateRoomForPlan(ctx, planID)
	if err != nil {
		return err
	}
	return s.repo.AddMember(ctx, room.ID, userID)
}

const (
	maxBodyLen   = 2000
	maxMedia     = 10
	maxVoiceSecs = 300
)

func validHTTPS(u string) bool {
	return len(u) > len("https://") && len(u) <= 2048 && strings.HasPrefix(u, "https://")
}

// validate enforces the per-type shape of a message before any DB work.
func (m *Message) validate() error {
	if m.RoomID == "" || m.SenderID == "" || len(m.Body) > maxBodyLen {
		return ErrInvalidInput
	}
	if m.Type == "" {
		m.Type = "text"
	}
	switch m.Type {
	case "text", "announcement":
		if strings.TrimSpace(m.Body) == "" || len(m.MediaIDs) > 0 || m.Location != nil {
			return ErrInvalidInput
		}
	case "image":
		if len(m.MediaIDs) < 1 || len(m.MediaIDs) > maxMedia {
			return ErrInvalidInput
		}
	case "voice":
		// Voice notes need audio uploads, which the media service doesn't accept
		// yet; refuse rather than accept an unplayable message.
		return ErrInvalidInput
	case "location":
		l := m.Location
		if l == nil || l.Latitude < -90 || l.Latitude > 90 || l.Longitude < -180 || l.Longitude > 180 || len(l.Label) > 200 {
			return ErrInvalidInput
		}
	default: // "poll" only via CreatePoll
		return ErrInvalidInput
	}
	return nil
}

// requireHost: the room's plan host or an admin. Ad-hoc rooms have no host,
// so only admins could announce there — effectively never.
func (s *Service) requireHost(ctx context.Context, roomID, userID, role string) error {
	if isAdmin(role) {
		return nil
	}
	host, err := s.repo.RoomHostID(ctx, roomID)
	if err != nil {
		return err
	}
	if host == "" || host != userID {
		return ErrNotHost
	}
	return nil
}

// screen returns ErrContentRejected for severe text and the auto-flag
// severity/reason for borderline text.
func (s *Service) screen(ctx context.Context, texts ...string) (severity, reason string, err error) {
	for _, t := range texts {
		if t == "" {
			continue
		}
		sev, why := s.screener.Screen(ctx, t)
		if sev == "severe" {
			return "", "", ErrContentRejected
		}
		if sev == "review" {
			severity, reason = sev, why
		}
	}
	return severity, reason, nil
}

func (s *Service) flag(ctx context.Context, messageID, senderID, severity, reason string) {
	if severity == "review" {
		// Fire-and-forget: a screening/queue failure must never block the
		// message the user already sent.
		_, _ = s.reporter.AutoFlagForSubject(ctx, "message", messageID, senderID, severity, reason)
	}
}

// SendMessage rejects senders who aren't a member of the room — the PRD §31
// "unauthorized users cannot read/send" guarantee. Membership rows are
// populated by cmd/worker on BOOKING_CONFIRMED (and by CreateRoom for hosts).
func (s *Service) SendMessage(ctx context.Context, m *Message, senderRole string) (*Message, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, m.RoomID, m.SenderID); err != nil {
		return nil, err
	}
	if blocked, err := s.repo.DMBlocked(ctx, m.RoomID, m.SenderID); err != nil {
		return nil, err
	} else if blocked {
		return nil, ErrNotAMember
	}
	if m.Type == "announcement" {
		if err := s.requireHost(ctx, m.RoomID, m.SenderID, senderRole); err != nil {
			return nil, err
		}
	}
	severity, reason, err := s.screen(ctx, m.Body)
	if err != nil {
		return nil, err
	}
	if len(m.MediaIDs) > 0 {
		if s.media == nil {
			return nil, ErrMediaUnavailable
		}
		m.Media, err = s.media.Claim(ctx, m.SenderID, m.MediaIDs)
		if err != nil {
			if errors.Is(err, media.ErrNotFound) {
				return nil, ErrInvalidInput
			}
			return nil, err
		}
	}
	sent, err := s.repo.SendMessage(ctx, m)
	if err != nil {
		return nil, err
	}
	s.flag(ctx, sent.ID, m.SenderID, severity, reason)
	s.repo.notifyDM(ctx, sent)
	return sent, nil
}

func (s *Service) CreatePoll(ctx context.Context, roomID, senderID, question string, options []string) (*Message, error) {
	question = strings.TrimSpace(question)
	if roomID == "" || senderID == "" || question == "" || len(question) > 300 || len(options) < 2 || len(options) > 6 {
		return nil, ErrInvalidInput
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(options))
	for _, o := range options {
		o = strings.TrimSpace(o)
		if o == "" || len(o) > 100 || seen[strings.ToLower(o)] {
			return nil, ErrInvalidInput
		}
		seen[strings.ToLower(o)] = true
		clean = append(clean, o)
	}
	if err := s.requireMember(ctx, roomID, senderID); err != nil {
		return nil, err
	}
	severity, reason, err := s.screen(ctx, append([]string{question}, clean...)...)
	if err != nil {
		return nil, err
	}
	msg, err := s.repo.CreatePoll(ctx, roomID, senderID, question, clean)
	if err != nil {
		return nil, err
	}
	s.flag(ctx, msg.ID, senderID, severity, reason)
	return msg, nil
}

// VotePoll checks membership of the room the poll actually belongs to.
func (s *Service) VotePoll(ctx context.Context, pollID, optionID, userID string) (*Poll, error) {
	if pollID == "" || optionID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	room, err := s.repo.PollRoom(ctx, pollID)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, room, userID); err != nil {
		return nil, err
	}
	if err := s.repo.Vote(ctx, pollID, userID, optionID); err != nil {
		return nil, err
	}
	return s.repo.GetPoll(ctx, pollID, userID)
}

// PinMessage: members who are the plan's host (or admins). The message must
// belong to the same room (enforced in SQL).
func (s *Service) PinMessage(ctx context.Context, roomID, messageID string, unpin bool, callerID, callerRole string) (string, error) {
	if roomID == "" || callerID == "" || (!unpin && messageID == "") {
		return "", ErrInvalidInput
	}
	if !isAdmin(callerRole) {
		if err := s.requireMember(ctx, roomID, callerID); err != nil {
			return "", err
		}
	}
	if err := s.requireHost(ctx, roomID, callerID, callerRole); err != nil {
		return "", err
	}
	if unpin {
		messageID = ""
	}
	if err := s.repo.SetPinned(ctx, roomID, messageID); err != nil {
		return "", err
	}
	return messageID, nil
}

func (s *Service) ListMessages(ctx context.Context, roomID, callerID string) ([]*Message, *Message, error) {
	msgs, pinned, _, err := s.ListMessagesPage(ctx, roomID, callerID, "")
	return msgs, pinned, err
}

// ListMessagesPage returns the newest page, or — given the token from the
// previous page — the page of messages older than it. next is empty on the last
// page. The pinned message is only returned with the first page.
func (s *Service) ListMessagesPage(ctx context.Context, roomID, callerID, pageToken string) (msgs []*Message, pinned *Message, next string, err error) {
	if roomID == "" || callerID == "" {
		return nil, nil, "", ErrInvalidInput
	}
	if err := s.requireMember(ctx, roomID, callerID); err != nil {
		return nil, nil, "", err
	}
	var before *time.Time
	if pageToken != "" {
		t, perr := time.Parse(time.RFC3339Nano, pageToken)
		if perr != nil {
			return nil, nil, "", ErrInvalidInput
		}
		before = &t
	}
	msgs, err = s.repo.ListMessages(ctx, roomID, callerID, defaultMessagePageSize+1, before)
	if err != nil {
		return nil, nil, "", err
	}
	if len(msgs) > defaultMessagePageSize {
		msgs = msgs[:defaultMessagePageSize]
		next = msgs[len(msgs)-1].SentAt.UTC().Format(time.RFC3339Nano)
	}
	if before == nil {
		if id, err := s.repo.PinnedMessageID(ctx, roomID); err == nil && id != "" {
			pinned, _ = s.repo.GetMessage(ctx, id, callerID)
		}
	}
	return msgs, pinned, next, nil
}

// ReportMessage requires the reporter to be a member of the message's room —
// otherwise message ids could be probed by anyone.
func (s *Service) ReportMessage(ctx context.Context, messageID, reporterID, reason string) (string, error) {
	if messageID == "" || reporterID == "" || reason == "" {
		return "", ErrInvalidInput
	}
	room, err := s.repo.MessageRoom(ctx, messageID)
	if err != nil {
		return "", err
	}
	if err := s.requireMember(ctx, room, reporterID); err != nil {
		return "", ErrMessageNotFound // don't confirm the message exists to non-members
	}
	return s.reporter.SubmitReportForSubject(ctx, reporterID, "message", messageID, reason)
}

// AddRoomMember adds a user to an existing room (idempotent). It performs no
// authorization itself — callers (e.g. internal/externalevents) must have
// established the user's right to join first.
func (s *Service) AddRoomMember(ctx context.Context, roomID, userID string) error {
	if roomID == "" || userID == "" {
		return ErrInvalidInput
	}
	return s.repo.AddMember(ctx, roomID, userID)
}

// Inbox splits the user's chats into Primary / General and totals unread for
// each (the bar badge shows both regardless of which tab is open).
// InboxResult is one inbox tab plus the unread totals for every tab, so the
// app can badge the bar and tabs from a single call.
type InboxResult struct {
	Chats                                      []Summary
	PrimaryUnread, GeneralUnread, GroupsUnread int32
}

// InboxFor returns the chats of a tab: "primary", "general", "all", or
// "groups" (plan / event / group rooms — everything that isn't a 1:1 DM).
func (s *Service) InboxFor(ctx context.Context, userID, tab string) (*InboxResult, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	all, err := s.repo.Inbox(ctx, userID, time.Now())
	if err != nil {
		return nil, err
	}
	out := &InboxResult{}
	for _, c := range all {
		if c.Primary {
			out.PrimaryUnread += c.Unread
		} else {
			out.GeneralUnread += c.Unread
		}
		if c.Kind != "dm" {
			out.GroupsUnread += c.Unread
		}
		if match := map[string]bool{"primary": c.Primary, "general": !c.Primary, "all": true, "groups": c.Kind != "dm"}[tab]; match {
			out.Chats = append(out.Chats, c)
		}
	}
	return out, nil
}

// Inbox is the two-tab form (Primary / General) kept for older callers.
func (s *Service) Inbox(ctx context.Context, userID string, primary bool) (chats []Summary, primaryUnread, generalUnread int32, err error) {
	tab := "general"
	if primary {
		tab = "primary"
	}
	r, err := s.InboxFor(ctx, userID, tab)
	if err != nil {
		return nil, 0, 0, err
	}
	return r.Chats, r.PrimaryUnread, r.GeneralUnread, nil
}

func (s *Service) MarkRead(ctx context.Context, roomID, userID string) error {
	if roomID == "" || userID == "" {
		return ErrInvalidInput
	}
	return s.repo.MarkRead(ctx, roomID, userID, time.Now())
}

// OpenDirectChat requires an accepted connection: flow.md §25 — messaging is
// never exposed to everyone, only to people who agreed to connect (accepting a
// request, or a mutual wave).
func (s *Service) OpenDirectChat(ctx context.Context, userID, otherID string) (string, error) {
	if userID == "" || otherID == "" || userID == otherID {
		return "", ErrInvalidInput
	}
	ok, err := s.repo.AreConnected(ctx, userID, otherID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotConnected
	}
	return s.repo.OpenDM(ctx, userID, otherID)
}

// OpenDM satisfies internal/meet's DMOpener (the connection already exists).
func (s *Service) OpenDM(ctx context.Context, a, b string) (string, error) {
	return s.OpenDirectChat(ctx, a, b)
}
