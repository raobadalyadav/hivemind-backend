package plans

import (
	"context"
	"errors"
	"time"

	"github.com/hivemind/backend/pkg/eventbus"
)

var (
	ErrInvalidInput   = errors.New("plans: invalid input")
	ErrForbidden      = errors.New("plans: caller is not the host of this plan")
	ErrAlreadyDecided = errors.New("plans: join request was already decided differently")
)

// CommunityRoleChecker is satisfied by *communities.Service — creating a
// community plan requires being that community's owner or moderator.
type CommunityRoleChecker interface {
	IsCommunityManager(ctx context.Context, communityID, userID string) (bool, error)
}

const (
	defaultSearchLimit  = 20
	defaultPlanDuration = 2 * time.Hour
	// startGrace lets a host publish a plan that starts "now" without
	// tripping over clock skew.
	startGrace = 5 * time.Minute
)

// BookingCreator/BookingCanceller are satisfied by *bookings.Service (wired
// in cmd/api/main.go) — declared here, not imported from internal/bookings,
// so neither package needs to import the other.
type BookingCreator interface {
	CreateBookingForPlan(ctx context.Context, planID, userID, idempotencyKey string) (bookingID string, err error)
}

type BookingCanceller interface {
	CancelBookingForPlan(ctx context.Context, planID, userID string) error
}

type Service struct {
	repo        *Repository
	creator     BookingCreator
	canceller   BookingCanceller
	draftGen    DraftGenerator
	communities CommunityRoleChecker
}

// WithCommunities enables community plans (setter, so NewService call sites
// stay unchanged).
func (s *Service) WithCommunities(c CommunityRoleChecker) *Service {
	s.communities = c
	return s
}

func NewService(repo *Repository, creator BookingCreator, canceller BookingCanceller, draftGen DraftGenerator) *Service {
	return &Service{repo: repo, creator: creator, canceller: canceller, draftGen: draftGen}
}

// SuggestPlanDraft is purely advisory — the host still calls CreatePlan
// with whatever title/description they end up choosing; this writes
// nothing.
func (s *Service) SuggestPlanDraft(ctx context.Context, categoryID string) (title, description string, err error) {
	categoryName, err := s.repo.GetCategoryName(ctx, categoryID)
	if err != nil {
		return "", "", err
	}
	title, description = s.draftGen.Suggest(ctx, categoryName)
	return title, description, nil
}

var (
	validJoinModes  = map[string]bool{"open": true, "approval": true, "invite_only": true}
	validVisibility = map[string]bool{"public": true, "private": true, "community": true}
)

func (s *Service) CreatePlan(ctx context.Context, p *Plan, recurrenceRule string) (*Plan, error) {
	if p.Title == "" || p.HostID == "" || p.Capacity <= 0 {
		return nil, ErrInvalidInput
	}
	if p.Currency == "" {
		p.Currency = "INR"
	}
	if p.JoinMode == "" {
		p.JoinMode = "open"
	}
	if p.Visibility == "" {
		p.Visibility = "public"
	}
	if !validJoinModes[p.JoinMode] || !validVisibility[p.Visibility] {
		return nil, ErrInvalidInput
	}
	// Invite-only plans can't be public (DB CHECK backstop), and a
	// community-visibility plan needs its community.
	if p.JoinMode == "invite_only" && p.Visibility == "public" {
		return nil, ErrInvalidInput
	}
	if p.Visibility == "community" && p.CommunityID == "" {
		return nil, ErrInvalidInput
	}
	if p.StartsAt.IsZero() {
		p.StartsAt = time.Now()
	}
	// A plan must have a real end: the completion/no-show jobs key off
	// ends_at, and a zero end (year 1) would make them fire immediately.
	if p.EndsAt.IsZero() {
		p.EndsAt = p.StartsAt.Add(defaultPlanDuration)
	}
	if !p.EndsAt.After(p.StartsAt) || p.StartsAt.Before(time.Now().Add(-startGrace)) {
		return nil, ErrInvalidInput
	}
	if recurrenceRule != "" {
		if _, err := ParseRule(recurrenceRule); err != nil {
			return nil, ErrInvalidInput
		}
	}

	if p.CommunityID != "" {
		if s.communities == nil {
			return nil, ErrInvalidInput
		}
		ok, err := s.communities.IsCommunityManager(ctx, p.CommunityID, p.HostID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrForbidden
		}
	}
	if p.RequiresEntitlement != "" {
		ok, err := s.repo.EntitlementProductExists(ctx, p.RequiresEntitlement)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrInvalidInput
		}
	}

	created, err := s.repo.Create(ctx, p, recurrenceRule)
	if err != nil {
		return nil, err
	}
	if recurrenceRule != "" {
		if _, err := s.repo.ExtendSeries(ctx, created.ID, time.Now()); err != nil {
			return nil, err
		}
	}
	return created, nil
}

// GetPlanAsUser is what handlers call: a hidden plan is indistinguishable
// from a nonexistent one (ErrPlanNotFound), so a private plan's existence
// can't be probed by guessing ids.
func (s *Service) GetPlanAsUser(ctx context.Context, id, callerID, callerRole string) (*Plan, error) {
	p, err := s.GetPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Visibility == "public" || p.HostID == callerID || isAdminRole(callerRole) {
		return p, nil
	}
	ok, err := s.repo.CanView(ctx, p, callerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrPlanNotFound
	}
	return p, nil
}

func (s *Service) requireHost(p *Plan, callerID, callerRole string) error {
	if p.HostID != callerID && !isAdminRole(callerRole) {
		return ErrForbidden
	}
	return nil
}

// RequestToJoinPlan is for approval-mode plans. The requester must already
// be able to see the plan (public plans: anyone; private: invitees).
func (s *Service) RequestToJoinPlan(ctx context.Context, planID, callerID, message string) (*JoinRequest, error) {
	if planID == "" || callerID == "" || len(message) > 500 {
		return nil, ErrInvalidInput
	}
	p, err := s.GetPlanAsUser(ctx, planID, callerID, "")
	if err != nil {
		return nil, err
	}
	if p.JoinMode != "approval" || p.HostID == callerID {
		return nil, ErrInvalidInput
	}
	jr, err := s.repo.CreateJoinRequest(ctx, planID, callerID, message)
	if err != nil {
		return nil, err
	}
	if jr.Status == "pending" {
		_ = s.repo.Notify(ctx, eventbus.NotifyUserPayload{
			UserID: p.HostID, Title: "New join request",
			Body: "Someone asked to join \"" + p.Title + "\".", DeepLink: "hivemind://plans/" + planID + "/requests", Channel: "push",
		})
	}
	return jr, nil
}

func (s *Service) ListPlanJoinRequests(ctx context.Context, planID, status, callerID, callerRole string) ([]*JoinRequest, error) {
	if planID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	p, err := s.repo.Get(ctx, planID)
	if err != nil {
		return nil, err
	}
	if err := s.requireHost(p, callerID, callerRole); err != nil {
		return nil, err
	}
	return s.repo.ListJoinRequests(ctx, planID, status, 100)
}

// RespondPlanJoinRequest: fetch request → its plan's host → compare to the
// caller. Repeating the same decision is a no-op; the opposite decision on
// an already-decided request is refused.
func (s *Service) RespondPlanJoinRequest(ctx context.Context, requestID, callerID, callerRole string, approve bool) (*JoinRequest, error) {
	if requestID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	existing, err := s.repo.GetJoinRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	p, err := s.repo.Get(ctx, existing.PlanID)
	if err != nil {
		return nil, err
	}
	if err := s.requireHost(p, callerID, callerRole); err != nil {
		return nil, err
	}
	jr, ok, err := s.repo.DecideJoinRequest(ctx, requestID, callerID, approve)
	if err != nil {
		return nil, err
	}
	if !ok {
		cur, err := s.repo.GetJoinRequest(ctx, requestID)
		if err != nil {
			return nil, err
		}
		if cur.Status != "pending" && (cur.Status == "approved") == approve {
			return cur, nil
		}
		return nil, ErrAlreadyDecided
	}
	title, body := "Request declined", "The host declined your request to join \""+p.Title+"\"."
	if approve {
		title, body = "Request approved", "You can now join \""+p.Title+"\"."
	}
	_ = s.repo.Notify(ctx, eventbus.NotifyUserPayload{
		UserID: jr.UserID, Title: title, Body: body, DeepLink: "hivemind://plans/" + p.ID, Channel: "push",
	})
	return jr, nil
}

const maxInvitesPerCall = 50

func (s *Service) InvitePlanUsers(ctx context.Context, planID, callerID, callerRole string, userIDs []string) (int, error) {
	if planID == "" || callerID == "" || len(userIDs) == 0 || len(userIDs) > maxInvitesPerCall {
		return 0, ErrInvalidInput
	}
	p, err := s.repo.Get(ctx, planID)
	if err != nil {
		return 0, err
	}
	if err := s.requireHost(p, callerID, callerRole); err != nil {
		return 0, err
	}
	invited, err := s.repo.InviteUsers(ctx, planID, callerID, userIDs)
	return len(invited), err
}

func (s *Service) RevokePlanInvite(ctx context.Context, planID, userID, callerID, callerRole string) error {
	if planID == "" || userID == "" || callerID == "" {
		return ErrInvalidInput
	}
	p, err := s.repo.Get(ctx, planID)
	if err != nil {
		return err
	}
	if err := s.requireHost(p, callerID, callerRole); err != nil {
		return err
	}
	return s.repo.RevokeInvite(ctx, planID, userID)
}

// ExtendAllSeries is the hourly worker job.
func (s *Service) ExtendAllSeries(ctx context.Context, now time.Time) (int, error) {
	return s.repo.ExtendAllSeries(ctx, now)
}

func (s *Service) GetPlan(ctx context.Context, id string) (*Plan, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

// GetPlanHostID satisfies promotions.PlanHostChecker — PurchasePromotion
// uses it to verify the caller actually hosts the plan being promoted,
// without importing this package's concrete Plan type.
func (s *Service) GetPlanHostID(ctx context.Context, id string) (string, error) {
	p, err := s.GetPlan(ctx, id)
	if err != nil {
		return "", err
	}
	return p.HostID, nil
}

func (s *Service) SearchPlans(ctx context.Context, f SearchFilter) ([]*Plan, error) {
	return s.repo.Search(ctx, f, defaultSearchLimit)
}

func (s *Service) JoinPlan(ctx context.Context, planID, userID, idempotencyKey string) (string, error) {
	if planID == "" || userID == "" {
		return "", ErrInvalidInput
	}
	return s.creator.CreateBookingForPlan(ctx, planID, userID, idempotencyKey)
}

func (s *Service) LeavePlan(ctx context.Context, planID, userID string) error {
	if planID == "" || userID == "" {
		return ErrInvalidInput
	}
	return s.canceller.CancelBookingForPlan(ctx, planID, userID)
}

func isAdminRole(role string) bool {
	return role == "admin" || role == "super_admin"
}

// CancelPlan requires the caller to be the plan's host or an admin — a
// plan-cancellation call from anyone else is rejected before any write
// happens (see pkg/grpcmiddleware's identical isAdminRole check; duplicated
// here rather than imported, since it's two lines and importing a
// middleware package into a service layer would be the wrong direction).
func (s *Service) CancelPlan(ctx context.Context, planID, callerID, callerRole, reason string, cancelFuture bool) (*Plan, error) {
	if planID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	plan, err := s.repo.Get(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan.HostID != callerID && !isAdminRole(callerRole) {
		return nil, ErrForbidden
	}
	// cancel_future cancels this occurrence and every later one, and stops
	// the series generating more (each cancel emits its own PLAN_CANCELLED,
	// so bookings/refunds fan out through the existing worker path).
	if cancelFuture && plan.SeriesID != "" {
		ids, err := s.repo.FutureOccurrenceIDs(ctx, plan)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, err := s.repo.Cancel(ctx, id, reason); err != nil {
				return nil, err
			}
		}
		if err := s.repo.EndSeriesFrom(ctx, plan.SeriesID, plan.StartsAt); err != nil {
			return nil, err
		}
		return s.repo.Get(ctx, planID)
	}
	return s.repo.Cancel(ctx, planID, reason)
}
