package smartgroups

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInvalidInput  = errors.New("smartgroups: invalid input")
	ErrForbidden     = errors.New("smartgroups: caller does not host this plan")
	ErrTooManyPeople = errors.New("smartgroups: too many confirmed participants to group")
)

// defaultGroupTargetSize matches flow.md §12's example (groups of 5-6).
const (
	defaultGroupTargetSize   = 6
	maxGroupableParticipants = 60
)

// PlanHostChecker is satisfied by *plans.Service (already exists, added
// for internal/promotions in Phase 3) — declared here so this package
// doesn't import plans concretely.
type PlanHostChecker interface {
	GetPlanHostID(ctx context.Context, planID string) (string, error)
}

// RoomCreator is satisfied by *chat.Service — same interface shape as
// internal/availability's, declared separately per the no-cross-import
// convention.
type RoomCreator interface {
	CreateAdHocRoomWithMembers(ctx context.Context, userIDs []string) (roomID string, err error)
}

func isAdminRole(role string) bool {
	return role == "admin" || role == "super_admin"
}

type Service struct {
	repo  *Repository
	hosts PlanHostChecker
	rooms RoomCreator
}

func NewService(repo *Repository, hosts PlanHostChecker, rooms RoomCreator) *Service {
	return &Service{repo: repo, hosts: hosts, rooms: rooms}
}

// GenerateSmartGroups requires the caller to host the plan (or be an
// admin). Idempotent: if groups already exist for this plan, they're
// returned as-is rather than regenerated — avoids creating duplicate chat
// rooms on a retried call and never trips the plan_groups
// UNIQUE(plan_id, label) constraint in normal operation.
func (s *Service) GenerateSmartGroups(ctx context.Context, planID, callerID, callerRole string) ([]*Group, error) {
	if planID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	hostID, err := s.hosts.GetPlanHostID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if hostID != callerID && !isAdminRole(callerRole) {
		return nil, ErrForbidden
	}

	existing, err := s.repo.ListForPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return existing, nil
	}

	participants, err := s.repo.ConfirmedParticipantsWithInterests(ctx, planID)
	if err != nil {
		return nil, err
	}
	if len(participants) > maxGroupableParticipants {
		return nil, ErrTooManyPeople
	}

	clusters := BuildGroups(participants, defaultGroupTargetSize)
	out := make([]*Group, 0, len(clusters))
	for i, cluster := range clusters {
		userIDs := make([]string, len(cluster))
		for j, p := range cluster {
			userIDs[j] = p.UserID
		}
		roomID, err := s.rooms.CreateAdHocRoomWithMembers(ctx, userIDs)
		if err != nil {
			return nil, err
		}
		label := fmt.Sprintf("Group %c", 'A'+i)
		groupID, err := s.repo.CreateGroup(ctx, planID, roomID, label, userIDs)
		if err != nil {
			return nil, err
		}
		out = append(out, &Group{ID: groupID, PlanID: planID, ChatRoomID: roomID, Label: label, MemberIDs: userIDs})
	}
	return out, nil
}

// ListSmartGroups requires the caller to be the plan's host, an admin, or
// a confirmed participant of the plan — group membership (who landed in
// which group) isn't public information.
func (s *Service) ListSmartGroups(ctx context.Context, planID, callerID, callerRole string) ([]*Group, error) {
	if planID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	hostID, err := s.hosts.GetPlanHostID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if hostID != callerID && !isAdminRole(callerRole) {
		isParticipant, err := s.repo.IsConfirmedParticipant(ctx, planID, callerID)
		if err != nil {
			return nil, err
		}
		if !isParticipant {
			return nil, ErrForbidden
		}
	}
	return s.repo.ListForPlan(ctx, planID)
}
