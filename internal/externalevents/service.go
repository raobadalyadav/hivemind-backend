package externalevents

import "context"

// RoomManager is satisfied by *chat.Service.
type RoomManager interface {
	CreateAdHocRoomWithMembers(ctx context.Context, userIDs []string) (roomID string, err error)
	AddRoomMember(ctx context.Context, roomID, userID string) error
}

type Service struct {
	repo  *Repository
	rooms RoomManager
}

func NewService(repo *Repository, rooms RoomManager) *Service {
	return &Service{repo: repo, rooms: rooms}
}

const (
	defaultPage = 20
	maxPeople   = 50
)

func (s *Service) List(ctx context.Context, userID, cityID, categoryID string, pageSize int32) ([]*Event, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	limit := int(pageSize)
	if limit <= 0 || limit > 50 {
		limit = defaultPage
	}
	return s.repo.List(ctx, userID, cityID, categoryID, limit)
}

func (s *Service) Get(ctx context.Context, userID, eventID string) (*Event, error) {
	if userID == "" || eventID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, userID, eventID)
}

// SetInterest is idempotent in both directions; the event must exist and be active.
func (s *Service) SetInterest(ctx context.Context, userID, eventID string, interested bool) (*Event, error) {
	if userID == "" || eventID == "" {
		return nil, ErrInvalidInput
	}
	if _, err := s.repo.Get(ctx, userID, eventID); err != nil {
		return nil, err
	}
	if err := s.repo.SetInterest(ctx, userID, eventID, interested); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, userID, eventID)
}

func (s *Service) requireInterested(ctx context.Context, userID, eventID string) error {
	if userID == "" || eventID == "" {
		return ErrInvalidInput
	}
	if _, err := s.repo.Get(ctx, userID, eventID); err != nil {
		return err
	}
	ok, err := s.repo.IsInterested(ctx, userID, eventID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotInterested
	}
	return nil
}

// ListInterestedPeople is visible only to people who are themselves interested.
func (s *Service) ListInterestedPeople(ctx context.Context, userID, eventID string) ([]Person, error) {
	if err := s.requireInterested(ctx, userID, eventID); err != nil {
		return nil, err
	}
	return s.repo.InterestedPeople(ctx, userID, eventID, maxPeople)
}

// JoinEventGroup lazily creates the event's shared room, then adds the
// caller. Concurrent first joiners each create a room but only one is
// recorded on the event (UPDATE ... COALESCE); the losers re-read the winner's
// room and join that.
// ponytail: a lost race leaves one orphan empty room (harmless, unreferenced);
// add a sweeper only if that ever shows up in volume.
func (s *Service) JoinEventGroup(ctx context.Context, userID, eventID string) (string, error) {
	if err := s.requireInterested(ctx, userID, eventID); err != nil {
		return "", err
	}
	room, err := s.repo.ExistingRoom(ctx, eventID)
	if err != nil {
		return "", err
	}
	if room == "" {
		mine, err := s.rooms.CreateAdHocRoomWithMembers(ctx, []string{userID})
		if err != nil {
			return "", err
		}
		if room, err = s.repo.ClaimRoom(ctx, eventID, mine); err != nil {
			return "", err
		}
	}
	if err := s.rooms.AddRoomMember(ctx, room, userID); err != nil {
		return "", err
	}
	return room, nil
}
