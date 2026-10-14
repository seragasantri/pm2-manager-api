package service

import (
	"context"
	"encoding/json"

	"github.com/labstack/echo/v4"
	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/repository"
)

// AuditService records and queries audit logs.
type AuditService struct {
	repo *repository.AuditRepository
}

func NewAuditService(r *repository.AuditRepository) *AuditService {
	return &AuditService{repo: r}
}

// Record inserts an audit entry. detail may be any JSON-marshalable value; marshaled to string.
func (s *AuditService) Record(ctx context.Context, claims *auth.Claims, action, resourceType, resourceID string, detail any, c echo.Context) error {
	var username, role, label string
	var userID *int64
	if claims != nil {
		role = claims.Role
		label = claims.Label
		if claims.Type == "admin" {
			username = "admin" // JWT admin has no username field, use role-derived label
			uid := claims.UserID
			userID = &uid
			// Try to use Subject as username if present
			if claims.Subject != "" {
				username = claims.Subject
			}
		} else {
			username = claims.Label
			tid := claims.TokenID
			userID = &tid
		}
		// Prefer label for display when available
		if username == "" {
			username = label
		}
		if username == "" {
			username = role
		}
	}
	ip := ""
	ua := ""
	if c != nil {
		ip = c.RealIP()
		if ip == "" {
			ip = c.Request().Header.Get("X-Real-IP")
		}
		ua = c.Request().Header.Get("User-Agent")
	}
	detailStr := ""
	if detail != nil {
		switch v := detail.(type) {
		case string:
			detailStr = v
		case []byte:
			detailStr = string(v)
		default:
			if b, err := json.Marshal(v); err == nil {
				detailStr = string(b)
			}
		}
		if len(detailStr) > 8000 {
			detailStr = detailStr[:8000]
		}
	}
	return s.repo.Create(ctx, repository.AuditLog{
		UserID:       userID,
		Username:     username,
		Role:         role,
		Label:        label,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Detail:       detailStr,
		IP:           ip,
		UserAgent:    ua,
	})
}

// List returns paginated logs.
func (s *AuditService) List(ctx context.Context, f repository.AuditFilter) ([]repository.AuditLog, int, error) {
	return s.repo.List(ctx, f)
}

// Live returns latest 50 entries (same as List page 1 limit 50).
func (s *AuditService) Live(ctx context.Context, limit int) ([]repository.AuditLog, int, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return s.repo.List(ctx, repository.AuditFilter{Page: 1, Limit: limit})
}

// Stats returns counts per action for last 7 days.
func (s *AuditService) Stats(ctx context.Context) (map[string]int, error) {
	return s.repo.Stats(ctx)
}

// EnsureTable creates the audit_logs table if missing.
func (s *AuditService) EnsureTable(ctx context.Context) error {
	return s.repo.EnsureTable(ctx)
}
