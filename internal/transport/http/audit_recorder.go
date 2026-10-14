package httpx

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/tragasolusi/pm2-manager-api/internal/auth"
)

var auditRecorder func(ctx context.Context, claims *auth.Claims, action, resourceType, resourceID string, detail any, c echo.Context) error

func SetAuditRecorder(fn func(ctx context.Context, claims *auth.Claims, action, resourceType, resourceID string, detail any, c echo.Context) error) {
	auditRecorder = fn
}

func auditRecord(c echo.Context, action, resourceType, resourceID string, detail any) {
	if auditRecorder == nil {
		return
	}
	claims := ClaimsFrom(c)
	// best-effort, never block request
	_ = auditRecorder(c.Request().Context(), claims, action, resourceType, resourceID, detail, c)
}
