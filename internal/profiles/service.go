package profiles

import (
	"context"
	"errors"
	"strings"
)

var ErrInvalidInput = errors.New("profiles: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

var validGenders = map[string]bool{"male": true, "female": true, "non_binary": true, "prefer_not_to_say": true}

func validHTTPSURL(u string) bool {
	return len(u) <= 2048 && strings.HasPrefix(u, "https://") && len(u) > len("https://")
}

func (s *Service) CreateProfile(ctx context.Context, p *Profile) (*Profile, error) {
	if p.UserID == "" || p.DisplayName == "" {
		return nil, ErrInvalidInput
	}
	if len(p.Interests) > 0 {
		norm, err := s.repo.NormalizeInterests(ctx, p.Interests)
		if err != nil {
			return nil, err
		}
		p.Interests = norm
	}
	return s.repo.Create(ctx, p)
}

func (s *Service) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, userID)
}

func tooLong(p *string, n int) bool { return p != nil && len(*p) > n }

func (s *Service) UpdateProfile(ctx context.Context, u *Update) (*Profile, error) {
	if u.UserID == "" || tooLong(u.Bio, 500) || tooLong(u.Occupation, 100) || tooLong(u.Education, 100) {
		return nil, ErrInvalidInput
	}
	if u.Gender != nil && *u.Gender != "" && !validGenders[*u.Gender] {
		return nil, ErrInvalidInput
	}
	if len(u.Languages) > 20 {
		return nil, ErrInvalidInput
	}
	if len(u.Interests) > 0 {
		norm, err := s.repo.NormalizeInterests(ctx, u.Interests)
		if err != nil {
			return nil, err
		}
		u.Interests = norm
	} else {
		u.Interests = nil
	}
	if len(u.Hobbies) > 0 {
		if len(u.Hobbies) > MaxInterests {
			return nil, ErrInvalidInput
		}
		for i, h := range u.Hobbies {
			h = strings.TrimSpace(h)
			if h == "" || len(h) > 40 {
				return nil, ErrInvalidInput
			}
			u.Hobbies[i] = h
		}
	} else {
		u.Hobbies = nil
	}
	if len(u.Languages) == 0 {
		u.Languages = nil
	}
	return s.repo.Update(ctx, u)
}

func (s *Service) SetPrivacy(ctx context.Context, userID string, showInPreviews bool) (*Profile, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.SetPrivacy(ctx, userID, showInPreviews)
}

func (s *Service) AddPhoto(ctx context.Context, userID, url string) (*Photo, error) {
	if userID == "" || !validHTTPSURL(url) {
		return nil, ErrInvalidInput
	}
	return s.repo.AddPhoto(ctx, userID, url)
}

func (s *Service) DeletePhoto(ctx context.Context, userID, photoID string) error {
	if userID == "" || photoID == "" {
		return ErrInvalidInput
	}
	return s.repo.DeletePhoto(ctx, userID, photoID)
}

func (s *Service) ReorderPhotos(ctx context.Context, userID string, ids []string) ([]Photo, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ReorderPhotos(ctx, userID, ids)
}

func (s *Service) InterestCatalog(ctx context.Context) ([]string, []string, error) {
	return s.repo.InterestCatalog(ctx)
}

func (s *Service) GetPrefs(ctx context.Context, userID string) (*Prefs, error) {
	return s.repo.GetPrefs(ctx, userID)
}

func (s *Service) SetIntents(ctx context.Context, userID string, intents []string) (*Prefs, error) {
	seen := map[string]bool{}
	clean := []string{}
	for _, i := range intents {
		if !validIntents[i] {
			return nil, ErrInvalidInput
		}
		if !seen[i] {
			seen[i] = true
			clean = append(clean, i)
		}
	}
	if err := s.repo.SetIntents(ctx, userID, clean); err != nil {
		return nil, err
	}
	return s.repo.GetPrefs(ctx, userID)
}

func (s *Service) SetPersonality(ctx context.Context, userID string, p *Prefs) (*Prefs, error) {
	if !validAnswer("group", p.GroupPref) || !validAnswer("energy", p.EnergyPref) ||
		!validAnswer("planning", p.PlanningPref) || !validAnswer("time", p.TimePref) ||
		!validAnswer("setting", p.SettingPref) {
		return nil, ErrInvalidInput
	}
	if err := s.repo.SetPersonality(ctx, userID, p); err != nil {
		return nil, err
	}
	return s.repo.GetPrefs(ctx, userID)
}
