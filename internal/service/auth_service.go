package service

import (
	"context"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/config"
	"github.com/tragasolusi/pm2-manager-api/internal/repository"
)

var (
	ErrInvalidCredentials = errors.New("username atau password salah")
	ErrInvalidAccessCode  = errors.New("kode akses tidak valid")
	ErrAccessDenied       = errors.New("akses ditolak")
)

type AuthService struct {
	users  *repository.UserRepository
	tokens *repository.TokenRepository
	jwt    *auth.Service
	cfg    *config.AppConfig
}

func NewAuthService(
	users *repository.UserRepository,
	tokens *repository.TokenRepository,
	jwt *auth.Service,
	cfg *config.AppConfig,
) *AuthService {
	return &AuthService{users: users, tokens: tokens, jwt: jwt, cfg: cfg}
}

type LoginResult struct {
	Token       string   `json:"token"`
	Role        string   `json:"role"`
	Label       string   `json:"label,omitempty"`
	AllowedApps []string `json:"allowedApps,omitempty"`
}

func (s *AuthService) LoginAdmin(ctx context.Context, username, password string) (*LoginResult, error) {
	u, err := s.users.FindByUsername(ctx, username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if u.Role != "superadmin" {
		return nil, ErrAccessDenied
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	tok, err := s.jwt.IssueAdmin(u.ID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: tok, Role: "superadmin"}, nil
}

func (s *AuthService) LoginWithToken(ctx context.Context, accessCode string) (*LoginResult, error) {
	t, err := s.tokens.FindByCode(ctx, accessCode)
	if err != nil {
		return nil, ErrInvalidAccessCode
	}
	tok, err := s.jwt.IssueTokenUser(t.ID, t.Label, t.AllowedApps, 12*time.Hour)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Token:       tok,
		Role:        "user",
		Label:       t.Label,
		AllowedApps: t.AllowedApps,
	}, nil
}
