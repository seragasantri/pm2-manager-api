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
	Token          string   `json:"token"`
	Role           string   `json:"role"`
	Label          string   `json:"label,omitempty"`
	AllowedApps    []string `json:"allowedApps,omitempty"`
	AllowedActions []string `json:"allowedActions,omitempty"`
}

func (s *AuthService) LoginAdmin(ctx context.Context, username, password string) (*LoginResult, error) {
	u, err := s.users.FindByUsername(ctx, username)
	if err != nil {
		// Hanya "user tidak ada" yang boleh jadi 401. Error lain (mis. MySQL mati
		// atau kredensial DB salah) harus dipropagasi supaya handler membalas 500
		// dan masalah infrastruktur tidak tersamar sebagai "password salah".
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
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
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrInvalidAccessCode
		}
		return nil, err
	}
	var tok string
	if len(t.AllowedActions) > 0 {
		tok, err = s.jwt.IssueTokenUserWithActions(t.ID, t.Label, t.AllowedApps, t.AllowedActions, 12*time.Hour)
	} else {
		tok, err = s.jwt.IssueTokenUser(t.ID, t.Label, t.AllowedApps, 12*time.Hour)
	}
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Token:          tok,
		Role:           "user",
		Label:          t.Label,
		AllowedApps:    t.AllowedApps,
		AllowedActions: t.AllowedActions,
	}, nil
}
