package httpx

import (
	"errors"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type AuthHandler struct {
	svc *service.AuthService
}

func NewAuthHandler(s *service.AuthService) *AuthHandler { return &AuthHandler{svc: s} }

type loginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	AccessCode string `json:"accessCode"`
}

func (h *AuthHandler) Login(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}

	if req.Username != "" && req.Password != "" {
		res, err := h.svc.LoginAdmin(c.Request().Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, service.ErrInvalidCredentials) || errors.Is(err, service.ErrAccessDenied) {
				return response.Unauthorized(c, err.Error())
			}
			return response.ServerError(c, err.Error())
		}
		return response.OK(c, res, "Login berhasil")
	}

	if req.AccessCode != "" {
		res, err := h.svc.LoginWithToken(c.Request().Context(), req.AccessCode)
		if err != nil {
			return response.Unauthorized(c, err.Error())
		}
		return response.OK(c, res, "Login berhasil")
	}

	return response.ValidationError(c, map[string]string{
		"general": "username/password atau accessCode diperlukan",
	})
}

func (h *AuthHandler) Me(c echo.Context) error {
	cl := ClaimsFrom(c)
	if cl == nil {
		return response.Unauthorized(c, "tidak terautentikasi")
	}
	data := map[string]any{
		"role":        cl.Role,
		"allowedApps": cl.AllowedApps,
		"label":       cl.Label,
	}
	return response.OK(c, data)
}
