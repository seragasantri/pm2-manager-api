package httpx

import (
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type LogHandler struct {
	svc *service.LogService
}

func NewLogHandler(s *service.LogService) *LogHandler { return &LogHandler{svc: s} }

// Index GET /logs?app=&level=&search=&page=&limit=
func (h *LogHandler) Index(c echo.Context) error {
	app := c.QueryParam("app")
	level := c.QueryParam("level")
	search := c.QueryParam("search")
	page, _ := strconv.Atoi(c.QueryParam("page"))
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	claims := ClaimsFrom(c)
	entries, total, err := h.svc.GetLogs(c.Request().Context(), app, level, search, page, limit, claims)
	if err != nil {
		if err == service.ErrLogNoAccess || err == service.ErrLogNoPerm {
			return response.Forbidden(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	if entries == nil {
		entries = []service.LogEntry{}
	}
	return response.OK(c, map[string]any{
		"logs":  entries,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// Stats GET /logs/stats?app=
func (h *LogHandler) Stats(c echo.Context) error {
	app := c.QueryParam("app")
	claims := ClaimsFrom(c)
	stats, err := h.svc.GetLogStats(c.Request().Context(), app, claims)
	if err != nil {
		if err == service.ErrLogNoAccess || err == service.ErrLogNoPerm {
			return response.Forbidden(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, stats)
}
