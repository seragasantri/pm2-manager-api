package httpx

import (
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/tragasolusi/pm2-manager-api/internal/repository"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

// AuditHandler exposes audit trail endpoints.
type AuditHandler struct {
	svc *service.AuditService
}

func NewAuditHandler(s *service.AuditService) *AuditHandler {
	return &AuditHandler{svc: s}
}

// Index GET /audit?user=&action=&resource_type=&search=&from=&to=&page=&limit=
func (h *AuditHandler) Index(c echo.Context) error {
	page, _ := strconv.Atoi(c.QueryParam("page"))
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	f := repository.AuditFilter{
		User:         c.QueryParam("user"),
		Action:       c.QueryParam("action"),
		ResourceType: c.QueryParam("resource_type"),
		Search:       c.QueryParam("search"),
		From:         c.QueryParam("from"),
		To:           c.QueryParam("to"),
		Page:         page,
		Limit:        limit,
	}
	logs, total, err := h.svc.List(c.Request().Context(), f)
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	if logs == nil {
		logs = []repository.AuditLog{}
	}
	return response.OK(c, map[string]any{
		"logs":  logs,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// Live GET /audit/live?limit=50 — latest N entries.
func (h *AuditHandler) Live(c echo.Context) error {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit < 1 {
		limit = 50
	}
	logs, total, err := h.svc.Live(c.Request().Context(), limit)
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	if logs == nil {
		logs = []repository.AuditLog{}
	}
	return response.OK(c, map[string]any{
		"logs":  logs,
		"total": total,
		"limit": limit,
	})
}

// Stats GET /audit/stats — counts per action last 7 days.
func (h *AuditHandler) Stats(c echo.Context) error {
	m, err := h.svc.Stats(c.Request().Context())
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, m)
}
