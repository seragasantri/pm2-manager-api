package service

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/repository"
)

var (
	ErrCurrentPasswordIncorrect = errors.New("password saat ini salah")
	ErrNewPasswordRequired      = errors.New("password baru diperlukan")
	ErrPasswordTooWeak          = errors.New("password minimal 6 karakter")
)

type SettingsService struct {
	users *repository.UserRepository
	jwt   *auth.Service
}

func NewSettingsService(users *repository.UserRepository, jwt *auth.Service) *SettingsService {
	return &SettingsService{users: users, jwt: jwt}
}

// GetSettings returns map{profile, notifications, appearance}.
func (s *SettingsService) GetSettings(ctx context.Context, claims *auth.Claims) (map[string]any, error) {
	if claims == nil {
		return nil, ErrAccessDenied
	}
	name := ""
	email := ""
	if claims.UserID > 0 {
		u, err := s.users.FindByID(ctx, claims.UserID)
		if err == nil && u != nil {
			name = u.Username
			// users table has no email column; expose username as name, email empty
			if strings.Contains(u.Username, "@") {
				email = u.Username
			}
		} else if claims.Role != "" {
			name = claims.Label
			if name == "" {
				name = "admin"
			}
		}
	} else {
		name = claims.Label
		if name == "" {
			name = claims.Role
		}
	}
	// ponytail: notifications/appearance are static defaults (no DB table). Upgrade: persist to settings table or user meta JSON.
	profile := map[string]any{
		"name":  name,
		"email": email,
	}
	notifications := map[string]any{
		"email": true,
		"push":  false,
	}
	appearance := map[string]any{
		"theme": "dark",
	}
	return map[string]any{
		"profile":       profile,
		"notifications": notifications,
		"appearance":    appearance,
	}, nil
}

// UpdateSettings applies partial updates (currently in-memory only).
func (s *SettingsService) UpdateSettings(ctx context.Context, claims *auth.Claims, input map[string]any) (map[string]any, error) {
	if claims == nil {
		return nil, ErrAccessDenied
	}
	current, err := s.GetSettings(ctx, claims)
	if err != nil {
		return nil, err
	}
	// merge allowed keys if present
	for _, key := range []string{"profile", "notifications", "appearance"} {
		if v, ok := input[key]; ok {
			current[key] = v
		}
	}
	return current, nil
}

// ChangePassword verifies current password via userRepo + bcrypt and updates.
func (s *SettingsService) ChangePassword(ctx context.Context, claims *auth.Claims, current, newPass string) error {
	if claims == nil || claims.UserID == 0 {
		return ErrAccessDenied
	}
	if strings.TrimSpace(current) == "" || strings.TrimSpace(newPass) == "" {
		return ErrNewPasswordRequired
	}
	if len(newPass) < 6 {
		return ErrPasswordTooWeak
	}
	u, err := s.users.FindByID(ctx, claims.UserID)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(current)); err != nil {
		return ErrCurrentPasswordIncorrect
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.users.UpdatePassword(ctx, claims.UserID, string(hashed))
}
