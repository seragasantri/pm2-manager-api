package httpx

import (
	"errors"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type SettingsHandler struct{ svc *service.SettingsService }

func NewSettingsHandler(s *service.SettingsService) *SettingsHandler {
	return &SettingsHandler{svc: s}
}

func (h *SettingsHandler) Index(c echo.Context) error {
	cl := ClaimsFrom(c)
	data, err := h.svc.GetSettings(c.Request().Context(), cl)
	if err != nil {
		if errors.Is(err, service.ErrAccessDenied) {
			return response.Forbidden(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, data)
}

func (h *SettingsHandler) Update(c echo.Context) error {
	cl := ClaimsFrom(c)
	var input map[string]any
	if err := c.Bind(&input); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	data, err := h.svc.UpdateSettings(c.Request().Context(), cl, input)
	if err != nil {
		if errors.Is(err, service.ErrAccessDenied) {
			return response.Forbidden(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, data, "Settings berhasil diupdate")
}

type passwordBody struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (h *SettingsHandler) ChangePassword(c echo.Context) error {
	cl := ClaimsFrom(c)
	var body passwordBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if strings.TrimSpace(body.CurrentPassword) == "" {
		return response.ValidationError(c, map[string]string{"currentPassword": "password saat ini diperlukan"})
	}
	if strings.TrimSpace(body.NewPassword) == "" {
		return response.ValidationError(c, map[string]string{"newPassword": "password baru diperlukan"})
	}
	if err := h.svc.ChangePassword(c.Request().Context(), cl, body.CurrentPassword, body.NewPassword); err != nil {
		if errors.Is(err, service.ErrCurrentPasswordIncorrect) {
			return response.ValidationError(c, map[string]string{"currentPassword": err.Error()})
		}
		if errors.Is(err, service.ErrNewPasswordRequired) || errors.Is(err, service.ErrPasswordTooWeak) {
			return response.ValidationError(c, map[string]string{"newPassword": err.Error()})
		}
		if errors.Is(err, service.ErrAccessDenied) {
			return response.Forbidden(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, nil, "Password berhasil diubah")
}
