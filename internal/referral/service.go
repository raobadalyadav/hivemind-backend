package referral

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetMyCode(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", ErrInvalidInput
	}
	return s.repo.GetOrCreateCode(ctx, userID)
}

func (s *Service) ApplyCode(ctx context.Context, userID, code string) error {
	code = normalize(code)
	if userID == "" || len(code) != 8 {
		return ErrInvalidInput
	}
	return s.repo.Apply(ctx, userID, code)
}

func (s *Service) Stats(ctx context.Context, userID string) (int32, int64, error) {
	if userID == "" {
		return 0, 0, ErrInvalidInput
	}
	return s.repo.Stats(ctx, userID)
}
