package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT body for our app. AllowedApps is only present for token-based users.
type Claims struct {
	Role        string   `json:"role"`
	Type        string   `json:"type"` // "admin" or "token"
	UserID      int64    `json:"userId,omitempty"`
	TokenID     int64    `json:"token_id,omitempty"`
	Label       string   `json:"label,omitempty"`
	AllowedApps []string `json:"allowedApps,omitempty"`
	jwt.RegisteredClaims
}

var (
	ErrInvalidToken = errors.New("token tidak valid")
	ErrExpired      = errors.New("sesi kadaluarsa")
)

// Service issues and verifies JWTs.
type Service struct {
	secret    []byte
	expiresIn time.Duration
}

func NewService(secret string, expiresIn time.Duration) *Service {
	return &Service{secret: []byte(secret), expiresIn: expiresIn}
}

func (s *Service) IssueAdmin(userID int64) (string, error) {
	claims := Claims{
		Role:   "superadmin",
		Type:   "admin",
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.expiresIn)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return s.sign(claims)
}

func (s *Service) IssueTokenUser(tokenID int64, label string, allowedApps []string, ttl time.Duration) (string, error) {
	claims := Claims{
		Role:        "user",
		Type:        "token",
		TokenID:     tokenID,
		Label:       label,
		AllowedApps: allowedApps,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return s.sign(claims)
}

func (s *Service) sign(claims Claims) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.secret)
}

// Verify parses a token string and returns the claims.
func (s *Service) Verify(tokenStr string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return s.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, ErrInvalidToken
	}
	if !tok.Valid {
		return nil, ErrInvalidToken
	}
	return c, nil
}
