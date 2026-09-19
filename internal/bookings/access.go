package bookings

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// accessError carries its own gRPC status, so a handler (or plans'
// mapBookingCreationError) can pass it straight through — these are
// PermissionDenied, not the FailedPrecondition every other booking error maps to.
type accessError struct {
	msg  string
	code codes.Code
}

func (e *accessError) Error() string { return e.msg }
func (e *accessError) GRPCStatus() *status.Status {
	return status.New(e.code, e.msg)
}

var (
	ErrInviteOnly          = &accessError{"bookings: this plan is invite-only", codes.PermissionDenied}
	ErrApprovalRequired    = &accessError{"bookings: the host must approve your request to join this plan", codes.PermissionDenied}
	ErrNotCommunityMember  = &accessError{"bookings: this plan is for community members only", codes.PermissionDenied}
	ErrEntitlementRequired = &accessError{"bookings: this premium plan requires an active subscription", codes.PermissionDenied}
)

// EntitlementChecker is satisfied by *subscriptions.Service (wired in
// cmd/api/main.go).
type EntitlementChecker interface {
	HasEntitlement(ctx context.Context, userID, key string) (bool, error)
}

// WithEntitlements enables premium-plan gating (setter, so NewService call
// sites stay unchanged).
func (s *Service) WithEntitlements(e EntitlementChecker) *Service {
	s.entitlements = e
	return s
}

type planAccess struct {
	JoinMode            string
	Visibility          string
	RequiresEntitlement string
	HostID              string
	Invited             bool
	Approved            bool
	CommunityMember     bool
}

func (r *Repository) getPlanAccess(ctx context.Context, planID, userID string) (*planAccess, error) {
	var a planAccess
	err := r.pool.QueryRow(ctx, `
		SELECT p.join_mode::text, p.visibility::text, COALESCE(p.requires_entitlement,''), p.host_id::text,
			EXISTS(SELECT 1 FROM plan_invites i WHERE i.plan_id = p.id AND i.user_id::text = $2),
			EXISTS(SELECT 1 FROM plan_join_requests r WHERE r.plan_id = p.id AND r.user_id::text = $2 AND r.status = 'approved'),
			(p.community_id IS NOT NULL AND EXISTS(
				SELECT 1 FROM community_members cm WHERE cm.community_id = p.community_id AND cm.user_id::text = $2))
		FROM plans p WHERE p.id = $1`, planID, userID,
	).Scan(&a.JoinMode, &a.Visibility, &a.RequiresEntitlement, &a.HostID, &a.Invited, &a.Approved, &a.CommunityMember)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPlanNotFound
	}
	return &a, err
}

// checkAccess is the single chokepoint enforcing plan types (flow.md §14).
// It runs for BookingService.CreateBooking, plans.JoinPlan and company team
// bookings (all funnel through CreateBooking) and for JoinWaitlist, and never
// touches capacity logic. The host always has access to their own plan.
func (s *Service) checkAccess(ctx context.Context, planID, userID string) error {
	a, err := s.repo.getPlanAccess(ctx, planID, userID)
	if err != nil {
		return err
	}
	if a.HostID == userID {
		return nil
	}
	if a.Visibility == "community" && !a.CommunityMember && !a.Invited {
		return ErrNotCommunityMember
	}
	switch a.JoinMode {
	case "invite_only":
		if !a.Invited {
			return ErrInviteOnly
		}
	case "approval":
		if !a.Approved && !a.Invited {
			return ErrApprovalRequired
		}
	}
	if a.RequiresEntitlement != "" {
		if s.entitlements == nil {
			return ErrEntitlementRequired
		}
		ok, err := s.entitlements.HasEntitlement(ctx, userID, a.RequiresEntitlement)
		if err != nil {
			return err
		}
		if !ok {
			return ErrEntitlementRequired
		}
	}
	return nil
}
