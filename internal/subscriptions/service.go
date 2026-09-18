package subscriptions

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetCatalog(ctx context.Context) ([]*Product, error) {
	return s.repo.ListProducts(ctx)
}
