package httpx

import (
	"errors"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type TokenHandler struct {
	svc *service.TokenService
}

func NewTokenHandler(s *service.TokenService) *TokenHandler { return &TokenHandler{svc: s} }

func (h *TokenHandler) Index(c echo.Context) error {
	tokens, err := h.svc.List(c.Request().Context())
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, tokens)
}

type tokenBody struct {
	Label       string   `json:"label"`
	AllowedApps []string `json:"allowedApps"`
}

func (h *TokenHandler) Store(c echo.Context) error {
	var body tokenBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if body.Label == "" {
		return response.ValidationError(c, map[string]string{"label": "label diperlukan"})
	}
	if len(body.AllowedApps) == 0 {
		return response.ValidationError(c, map[string]string{"allowedApps": "minimal 1 aplikasi harus dipilih"})
	}

	cl := ClaimsFrom(c)
	var createdBy *int64
	if cl != nil && cl.UserID > 0 {
		v := cl.UserID
		createdBy = &v
	}

	tok, err := h.svc.Create(c.Request().Context(), service.CreateTokenInput{
		Label: body.Label, AllowedApps: body.AllowedApps,
	}, createdBy)
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.Created(c, tok, "Token berhasil dibuat")
}

func (h *TokenHandler) Update(c echo.Context) error {
	id, err := parseID(c, "id")
	if err != nil {
		return response.ValidationError(c, err.Error())
	}
	var body tokenBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}

	tok, err := h.svc.Update(c.Request().Context(), id, service.CreateTokenInput{
		Label: body.Label, AllowedApps: body.AllowedApps,
	})
	if err != nil {
		if errors.Is(err, service.ErrTokenNotFound) {
			return response.NotFound(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, tok, "Token berhasil diupdate")
}

func (h *TokenHandler) Destroy(c echo.Context) error {
	id, err := parseID(c, "id")
	if err != nil {
		return response.ValidationError(c, err.Error())
	}
	if err := h.svc.Delete(c.Request().Context(), id); err != nil {
		if errors.Is(err, service.ErrTokenNotFound) {
			return response.NotFound(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, nil, "Token berhasil dihapus")
}