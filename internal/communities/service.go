package communities

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrInvalidInput        = errors.New("communities: invalid input")
	ErrForbidden           = errors.New("communities: you must own or moderate this community")
	ErrEntitlementRequired = errors.New("communities: this paid community requires an active subscription")
	ErrAlreadyDecided      = errors.New("communities: join request was already decided differently")
	ErrOwnerCannotLeave    = errors.New("communities: the owner cannot leave their community")
)

// EntitlementChecker is satisfied by *subscriptions.Service. A paid
// community records entitlement-gated access only — no billing happens here.
type EntitlementChecker interface {
	HasEntitlement(ctx context.Context, userID, key string) (bool, error)
}

type Service struct {
	repo         *Repository
	entitlements EntitlementChecker
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) WithEntitlements(e EntitlementChecker) *Service {
	s.entitlements = e
	return s
}

var validTypes = map[string]bool{"public": true, "private": true, "approval": true, "paid": true}

func validMediaURL(u string) bool {
	return u == "" || (strings.HasPrefix(u, "https://") && len(u) <= 2048)
}

func (s *Service) CreateCommunity(ctx context.Context, c *Community) (*Community, error) {
	if c.Name == "" || c.OwnerID == "" {
		return nil, ErrInvalidInput
	}
	if c.MembershipType == "" {
		c.MembershipType = "public"
	}
	if !validTypes[c.MembershipType] || !validMediaURL(c.CoverImageURL) || len(c.Rules) > 5000 {
		return nil, ErrInvalidInput
	}
	if c.MembershipType == "paid" && c.RequiredEntitlement == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, c)
}

// canView hides private communities from everyone but members and invitees;
// a hidden community is indistinguishable from a nonexistent one.
func (s *Service) canView(ctx context.Context, c *Community, userID string) (bool, error) {
	if c.MembershipType != "private" {
		return true, nil
	}
	role, err := s.repo.MemberRole(ctx, c.ID, userID)
	if err != nil {
		return false, err
	}
	if role != "" {
		return true, nil
	}
	return s.repo.HasApprovedInvite(ctx, c.ID, userID)
}

func (s *Service) GetCommunity(ctx context.Context, id, callerID string) (*Community, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	ok, err := s.canView(ctx, c, callerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrCommunityNotFound
	}
	if err := s.repo.MarkMembership(ctx, []*Community{c}, callerID); err != nil {
		return nil, err
	}
	return c, nil
}

const defaultPlanPageSize = 20

// JoinCommunity behaves by community type: public joins immediately;
// approval files a pending request; private needs an invite; paid needs the
// community's entitlement.
func (s *Service) JoinCommunity(ctx context.Context, communityID, userID string) (*Membership, error) {
	if communityID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	c, err := s.repo.Get(ctx, communityID)
	if err != nil {
		return nil, err
	}
	if role, err := s.repo.MemberRole(ctx, communityID, userID); err != nil {
		return nil, err
	} else if role != "" {
		return &Membership{CommunityID: communityID, UserID: userID, Role: role, Status: "active"}, nil
	}

	switch c.MembershipType {
	case "approval":
		st, err := s.repo.RequestToJoin(ctx, communityID, userID)
		if err != nil {
			return nil, err
		}
		if st == "approved" { // e.g. invited, or approved earlier
			return s.repo.Join(ctx, communityID, userID)
		}
		return &Membership{CommunityID: communityID, UserID: userID, Role: "", Status: st}, nil
	case "private":
		ok, err := s.repo.HasApprovedInvite(ctx, communityID, userID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrCommunityNotFound
		}
	case "paid":
		if s.entitlements == nil {
			return nil, ErrEntitlementRequired
		}
		ok, err := s.entitlements.HasEntitlement(ctx, userID, c.RequiredEntitlement)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrEntitlementRequired
		}
	}
	return s.repo.Join(ctx, communityID, userID)
}

func (s *Service) LeaveCommunity(ctx context.Context, communityID, userID string) error {
	if communityID == "" || userID == "" {
		return ErrInvalidInput
	}
	role, err := s.repo.MemberRole(ctx, communityID, userID)
	if err != nil {
		return err
	}
	if role == "owner" {
		return ErrOwnerCannotLeave
	}
	return s.repo.Leave(ctx, communityID, userID)
}

func (s *Service) ListCommunities(ctx context.Context, cityID, categoryID, query, viewerID string, onlyMine bool) ([]*Community, error) {
	if onlyMine && viewerID == "" {
		return nil, ErrInvalidInput
	}
	list, err := s.repo.List(ctx, cityID, categoryID, query, viewerID, onlyMine, 50)
	if err != nil {
		return nil, err
	}
	return list, s.repo.MarkMembership(ctx, list, viewerID)
}

func (s *Service) ListCommunityPlans(ctx context.Context, communityID, callerID string) ([]string, error) {
	if communityID == "" {
		return nil, ErrInvalidInput
	}
	c, err := s.repo.Get(ctx, communityID)
	if err != nil {
		return nil, err
	}
	ok, err := s.canView(ctx, c, callerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrCommunityNotFound
	}
	role, err := s.repo.MemberRole(ctx, communityID, callerID)
	if err != nil {
		return nil, err
	}
	return s.repo.ListPlanIDs(ctx, communityID, role != "", defaultPlanPageSize)
}

// IsCommunityManager satisfies plans.CommunityRoleChecker.
func (s *Service) IsCommunityManager(ctx context.Context, communityID, userID string) (bool, error) {
	role, err := s.repo.MemberRole(ctx, communityID, userID)
	if err != nil {
		return false, err
	}
	return role == "owner" || role == "moderator", nil
}

func (s *Service) requireManager(ctx context.Context, communityID, userID string) error {
	ok, err := s.IsCommunityManager(ctx, communityID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

func (s *Service) ListJoinRequests(ctx context.Context, communityID, status, callerID string) ([]*JoinRequest, error) {
	if communityID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	if err := s.requireManager(ctx, communityID, callerID); err != nil {
		return nil, err
	}
	return s.repo.ListRequests(ctx, communityID, status, 100)
}

// RespondJoinRequest: fetch request → its community → require owner/
// moderator. Repeating the same decision is a no-op.
func (s *Service) RespondJoinRequest(ctx context.Context, requestID, callerID string, approve bool) (*JoinRequest, error) {
	if requestID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	existing, err := s.repo.GetRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if err := s.requireManager(ctx, existing.CommunityID, callerID); err != nil {
		return nil, err
	}
	jr, ok, err := s.repo.DecideRequest(ctx, requestID, callerID, approve)
	if err != nil {
		return nil, err
	}
	if !ok {
		cur, err := s.repo.GetRequest(ctx, requestID)
		if err != nil {
			return nil, err
		}
		if cur.Status != "pending" && (cur.Status == "approved") == approve {
			return cur, nil
		}
		return nil, ErrAlreadyDecided
	}
	return jr, nil
}

func (s *Service) InviteToCommunity(ctx context.Context, communityID, callerID, userID string) error {
	if communityID == "" || callerID == "" || userID == "" {
		return ErrInvalidInput
	}
	if err := s.requireManager(ctx, communityID, callerID); err != nil {
		return err
	}
	return s.repo.Invite(ctx, communityID, userID, callerID)
}
