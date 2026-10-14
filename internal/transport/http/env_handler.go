package httpx

import (
	"regexp"

	"github.com/labstack/echo/v4"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

var envKeyRe2 = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type EnvHandler struct {
	svc *service.EnvService
}

func NewEnvHandler(s *service.EnvService) *EnvHandler { return &EnvHandler{svc: s} }

func (h *EnvHandler) Index(c echo.Context) error {
	appName := c.Param("appName")
	if appName == "" {
		appName = c.QueryParam("appName")
	}
	if appName == "" {
		return response.ValidationError(c, map[string]string{"appName": "appName diperlukan"})
	}
	claims := ClaimsFrom(c)
	vars, err := h.svc.GetEnvVars(c.Request().Context(), appName, claims)
	if err != nil {
		if err == service.ErrEnvNoAccess {
			return response.Forbidden(c, err.Error())
		}
		if err == service.ErrEnvAppNotFound {
			return response.NotFound(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	if vars == nil {
		vars = []service.EnvVar{}
	}
	return response.OK(c, vars)
}

type envUpdateRequest struct {
	Vars []service.EnvVar `json:"vars"`
}

func (h *EnvHandler) Update(c echo.Context) error {
	appName := c.Param("appName")
	if appName == "" {
		appName = c.QueryParam("appName")
	}
	if appName == "" {
		return response.ValidationError(c, map[string]string{"appName": "appName diperlukan"})
	}
	var req envUpdateRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if req.Vars == nil {
		return response.ValidationError(c, map[string]string{"vars": "vars diperlukan"})
	}
	for _, v := range req.Vars {
		if !envKeyRe2.MatchString(v.Key) {
			return response.ValidationError(c, map[string]string{"key": "key tidak valid: " + v.Key})
		}
	}
	claims := ClaimsFrom(c)
	if err := h.svc.UpdateEnvVars(c.Request().Context(), appName, req.Vars, claims); err != nil {
		if err == service.ErrEnvNoAccess {
			return response.Forbidden(c, err.Error())
		}
		if err == service.ErrEnvAppNotFound {
			return response.NotFound(c, err.Error())
		}
		if err == service.ErrEnvInvalidKey {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	auditRecord(c, "env.update", "env", appName, map[string]any{"vars_count": len(req.Vars)})
	return response.OK(c, map[string]string{"message": "env berhasil disimpan"}, "env berhasil disimpan")
}
