package service

import (
	"context"
	"errors"
	"strings"

	"github.com/tragasolusi/pm2-manager-api/internal/repository"
)

var (
	ErrLabelRequired    = errors.New("label diperlukan")
	ErrAppsRequired     = errors.New("minimal 1 aplikasi harus dipilih")
	ErrTokenNotFound    = errors.New("token tidak ditemukan")
)

type TokenService struct {
	repo *repository.TokenRepository
}

func NewTokenService(r *repository.TokenRepository) *TokenService {
	return &TokenService{repo: r}
}

func (s *TokenService) List(ctx context.Context) (any, error) {
	return s.repo.All(ctx)
}

type CreateTokenInput struct {
	Label       string
	AllowedApps []string
}

func (s *TokenService) Create(ctx context.Context, in CreateTokenInput, createdBy *int64) (any, error) {
	label := strings.TrimSpace(in.Label)
	if label == "" {
		return nil, ErrLabelRequired
	}
	if len(in.AllowedApps) == 0 {
		return nil, ErrAppsRequired
	}
	return s.repo.Create(ctx, label, in.AllowedApps, createdBy)
}

func (s *TokenService) Update(ctx context.Context, id int64, in CreateTokenInput) (any, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrTokenNotFound
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = existing.Label
	}
	if len(in.AllowedApps) == 0 {
		in.AllowedApps = existing.AllowedApps
	}
	if err := s.repo.Update(ctx, id, label, in.AllowedApps); err != nil {
		return nil, err
	}
	return s.repo.FindByID(ctx, id)
}

func (s *TokenService) Delete(ctx context.Context, id int64) error {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		return ErrTokenNotFound
	}
	return s.repo.Delete(ctx, id)
}
