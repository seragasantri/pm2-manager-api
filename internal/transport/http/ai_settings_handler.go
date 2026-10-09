package httpx

import (
	"errors"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

// AiSettingsHandler: pengaturan provider AI + playground (khusus superadmin,
// dipasang di grup admin).
type AiSettingsHandler struct {
	svc *service.AiSettingsService
}

func NewAiSettingsHandler(s *service.AiSettingsService) *AiSettingsHandler {
	return &AiSettingsHandler{svc: s}
}

func (h *AiSettingsHandler) Status(c echo.Context) error {
	data, err := h.svc.Status(c.Request().Context())
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, data)
}

type aiFetchBody struct {
	BaseURL string `json:"base_url"`
	ApiKey  string `json:"api_key"`
}

func (h *AiSettingsHandler) FetchModels(c echo.Context) error {
	var body aiFetchBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	models, err := h.svc.FetchModels(c.Request().Context(), body.BaseURL, body.ApiKey)
	if err != nil {
		if errors.Is(err, service.ErrAiEndpointWajib) {
			return response.ValidationError(c, map[string]string{"general": err.Error()})
		}
		return response.ServerError(c, err.Error())
	}
	if len(models) == 0 {
		return response.ValidationError(c, map[string]string{"general": "Endpoint tidak mengembalikan model apa pun"})
	}
	return response.OK(c, models)
}

type aiSaveBody struct {
	BaseURL string `json:"base_url"`
	ApiKey  string `json:"api_key"`
	Model   string `json:"model"`
}

func (h *AiSettingsHandler) Save(c echo.Context) error {
	var body aiSaveBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if err := h.svc.Save(c.Request().Context(), body.BaseURL, body.ApiKey, body.Model); err != nil {
		if errors.Is(err, service.ErrAiModelWajib) || errors.Is(err, service.ErrAiEndpointInvalid) {
			return response.ValidationError(c, map[string]string{"general": err.Error()})
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, map[string]string{"status": "tersimpan"}, "Pengaturan AI berhasil disimpan")
}

func (h *AiSettingsHandler) CekKoneksi(c echo.Context) error {
	result, err := h.svc.CekKoneksi(c.Request().Context())
	if err != nil {
		if errors.Is(err, service.ErrAiBelumDikonfigurasi) {
			return response.ValidationError(c, map[string]string{"general": err.Error()})
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, result)
}

type aiPlaygroundBody struct {
	History      []service.ChatMessage `json:"history"`
	SystemPrompt string                `json:"system_prompt"`
}

func (h *AiSettingsHandler) Playground(c echo.Context) error {
	var body aiPlaygroundBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	result, err := h.svc.Playground(c.Request().Context(), body.History, body.SystemPrompt)
	if err != nil {
		if errors.Is(err, service.ErrAiBelumDikonfigurasi) {
			return response.ValidationError(c, map[string]string{"general": err.Error()})
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, result)
}
