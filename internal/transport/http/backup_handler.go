package httpx

import (
	"errors"

	"github.com/labstack/echo/v4"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type BackupHandler struct {
	svc *service.BackupService
}

func NewBackupHandler(s *service.BackupService) *BackupHandler { return &BackupHandler{svc: s} }

// Index GET /backups
func (h *BackupHandler) Index(c echo.Context) error {
	data, err := h.svc.GetBackups(c.Request().Context())
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	if data == nil {
		data = []service.BackupProject{}
	}
	return response.OK(c, data)
}

// Log GET /backups/log?date=&type=projects|hermes
func (h *BackupHandler) Log(c echo.Context) error {
	date := c.QueryParam("date")
	typ := c.QueryParam("type")
	if typ == "" {
		typ = "projects"
	}
	content, err := h.svc.GetBackupLog(c.Request().Context(), date, typ)
	if err != nil {
		if errors.Is(err, service.ErrBackupLogNotFound) {
			return response.NotFound(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, map[string]any{
		"date":    date,
		"type":    typ,
		"content": content,
	})
}
