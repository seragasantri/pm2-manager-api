package httpx

import (
	"github.com/labstack/echo/v4"
	statsSvc "github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type StatsHandler struct {
	svc *statsSvc.StatsService
}

func NewStatsHandler(s *statsSvc.StatsService) *StatsHandler { return &StatsHandler{svc: s} }

func (h *StatsHandler) Stats(c echo.Context) error {
	data, err := h.svc.GetStats()
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, data)
}
