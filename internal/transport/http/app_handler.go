package httpx

import (
	"errors"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type AppHandler struct {
	svc *service.AppService
}

func NewAppHandler(s *service.AppService) *AppHandler { return &AppHandler{svc: s} }

func (h *AppHandler) Index(c echo.Context) error {
	apps, err := h.svc.GetApps(c.Request().Context(), ClaimsFrom(c))
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, apps)
}

type appActionRequest struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

func (h *AppHandler) Action(c echo.Context) error {
	var req appActionRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if req.Name == "" {
		return response.ValidationError(c, map[string]string{"name": "name diperlukan"})
	}
	if req.Action == "" {
		return response.ValidationError(c, map[string]string{"action": "action diperlukan"})
	}

	if err := h.svc.DoAction(c.Request().Context(), req.Name, req.Action, ClaimsFrom(c)); err != nil {
		if errors.Is(err, service.ErrActionInvalid) {
			return response.ValidationError(c, map[string]string{"action": "action harus start, stop, atau restart"})
		}
		if errors.Is(err, service.ErrNoAccessToApp) {
			return response.Forbidden(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, nil, "Aplikasi '"+req.Name+"' berhasil di-"+req.Action)
}
