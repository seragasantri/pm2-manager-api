package httpx

import (
	"errors"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type CronHandler struct{ svc *service.CronService }

func NewCronHandler(s *service.CronService) *CronHandler { return &CronHandler{svc: s} }

func (h *CronHandler) Index(c echo.Context) error {
	jobs, err := h.svc.GetCronJobs(c.Request().Context())
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, jobs)
}

type cronBody struct {
	Command  string `json:"command"`
	Schedule string `json:"schedule"`
	Name     string `json:"name"`
}

func (h *CronHandler) Store(c echo.Context) error {
	var body cronBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if strings.TrimSpace(body.Command) == "" {
		return response.ValidationError(c, map[string]string{"command": "command diperlukan"})
	}
	if strings.TrimSpace(body.Schedule) == "" {
		return response.ValidationError(c, map[string]string{"schedule": "schedule diperlukan"})
	}
	job, err := h.svc.CreateCronJob(c.Request().Context(), body.Command, body.Schedule, body.Name)
	if err != nil {
		if errors.Is(err, service.ErrCronInvalidSch) || errors.Is(err, service.ErrCronCmdRequired) {
			return response.ValidationError(c, map[string]string{"schedule": err.Error()})
		}
		return response.ServerError(c, err.Error())
	}
	auditRecord(c, "cron.create", "cron", job.ID, body)
	return response.Created(c, job, "Cron job berhasil dibuat")
}

func (h *CronHandler) Update(c echo.Context) error {
	id := c.Param("id")
	if strings.TrimSpace(id) == "" {
		return response.ValidationError(c, "id diperlukan")
	}
	var body cronBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	job, err := h.svc.UpdateCronJob(c.Request().Context(), id, body.Command, body.Schedule, body.Name)
	if err != nil {
		if errors.Is(err, service.ErrCronNotFound) {
			return response.NotFound(c, err.Error())
		}
		if errors.Is(err, service.ErrCronInvalidSch) || errors.Is(err, service.ErrCronCmdRequired) {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	auditRecord(c, "cron.update", "cron", id, body)
	return response.OK(c, job, "Cron job berhasil diupdate")
}

func (h *CronHandler) Destroy(c echo.Context) error {
	id := c.Param("id")
	if strings.TrimSpace(id) == "" {
		return response.ValidationError(c, "id diperlukan")
	}
	if err := h.svc.DeleteCronJob(c.Request().Context(), id); err != nil {
		if errors.Is(err, service.ErrCronNotFound) {
			return response.NotFound(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	auditRecord(c, "cron.delete", "cron", id, nil)
	return response.OK(c, nil, "Cron job berhasil dihapus")
}

type toggleBody struct {
	Enabled *bool `json:"enabled"`
}

func (h *CronHandler) Toggle(c echo.Context) error {
	id := c.Param("id")
	if strings.TrimSpace(id) == "" {
		return response.ValidationError(c, "id diperlukan")
	}
	var body toggleBody
	if err := c.Bind(&body); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if body.Enabled == nil {
		return response.ValidationError(c, map[string]string{"enabled": "enabled diperlukan"})
	}
	job, err := h.svc.ToggleCronJob(c.Request().Context(), id, *body.Enabled)
	if err != nil {
		if errors.Is(err, service.ErrCronNotFound) {
			return response.NotFound(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	auditRecord(c, "cron.toggle", "cron", id, body)
	return response.OK(c, job, "Cron job berhasil diupdate")
}
