package httpx

import (
	"errors"

	"github.com/labstack/echo/v4"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type UptimeHandler struct {
	svc *service.UptimeService
}

func NewUptimeHandler(s *service.UptimeService) *UptimeHandler { return &UptimeHandler{svc: s} }

// Index GET /uptime
func (h *UptimeHandler) Index(c echo.Context) error {
	data, err := h.svc.CheckAll(c.Request().Context())
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, data)
}

// Show GET /uptime/:domain
func (h *UptimeHandler) Show(c echo.Context) error {
	domain := c.Param("domain")
	t, err := h.svc.CheckOne(c.Request().Context(), domain)
	if err != nil {
		if errors.Is(err, service.ErrTargetNotFound) {
			return response.NotFound(c, err.Error())
		}
		if err.Error() == "domain diperlukan" {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, t)
}
