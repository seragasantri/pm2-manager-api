package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type GitHandler struct {
	svc *service.GitService
}

func NewGitHandler(s *service.GitService) *GitHandler { return &GitHandler{svc: s} }

func (h *GitHandler) checkAccess(c echo.Context, appName string) error {
	cl := ClaimsFrom(c)
	if cl == nil {
		return response.Unauthorized(c, "tidak terautentikasi")
	}
	if cl.Role == "superadmin" {
		return nil
	}
	for _, a := range cl.AllowedApps {
		if a == appName {
			return nil
		}
	}
	return response.Forbidden(c, "anda tidak memiliki akses ke aplikasi ini")
}

// Status GET /api/git/status?appName=X
func (h *GitHandler) Status(c echo.Context) error {
	appName := c.QueryParam("appName")
	if appName == "" {
		return response.ValidationError(c, map[string]string{"appName": "appName diperlukan"})
	}
	if err := h.checkAccess(c, appName); err != nil {
		return err
	}
	st, err := h.svc.Status(c.Request().Context(), appName, ClaimsFrom(c), nil)
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, st)
}

// Branches GET /api/git/branches?appName=X
func (h *GitHandler) Branches(c echo.Context) error {
	appName := c.QueryParam("appName")
	if appName == "" {
		return response.ValidationError(c, map[string]string{"appName": "appName diperlukan"})
	}
	if err := h.checkAccess(c, appName); err != nil {
		return err
	}
	branches, err := h.svc.Branches(c.Request().Context(), appName, ClaimsFrom(c))
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, branches)
}

// Diff GET /api/git/diff?appName=X
func (h *GitHandler) Diff(c echo.Context) error {
	appName := c.QueryParam("appName")
	if appName == "" {
		return response.ValidationError(c, map[string]string{"appName": "appName diperlukan"})
	}
	if err := h.checkAccess(c, appName); err != nil {
		return err
	}
	d, err := h.svc.Diff(c.Request().Context(), appName, ClaimsFrom(c))
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, d)
}

type pullRequest struct {
	AppName string `json:"appName"`
	Branch  string `json:"branch"`
}

// Pull POST /api/git/pull — streams SSE log + final result.
func (h *GitHandler) Pull(c echo.Context) error {
	var req pullRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if req.AppName == "" {
		return response.ValidationError(c, map[string]string{"appName": "appName diperlukan"})
	}
	if err := h.checkAccess(c, req.AppName); err != nil {
		return err
	}

	c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().Header().Set("X-Accel-Buffering", "no")
	c.Response().WriteHeader(http.StatusOK)

	flusher, ok := c.Response().Writer.(http.Flusher)

	writeEvent := func(event, data string) {
		// SSE data lines cannot contain raw newlines unescaped — split
		data = strings.ReplaceAll(data, "\r\n", "\n")
		_, _ = fmt.Fprintf(c.Response(), "event: %s\n", event)
		for _, line := range strings.Split(data, "\n") {
			_, _ = fmt.Fprintf(c.Response(), "data: %s\n", line)
		}
		_, _ = fmt.Fprintf(c.Response(), "\n")
		if ok {
			flusher.Flush()
		}
	}

	sw := &sseWriter{emit: writeEvent}
	writeEvent("log", fmt.Sprintf("Starting git pull + deploy for %s...\n", req.AppName))

	result, err := h.svc.Pull(c.Request().Context(), req.AppName, req.Branch, ClaimsFrom(c), sw)
	if err != nil {
		writeEvent("error", err.Error())
		writeEvent("done", `{"success":false}`)
		return nil
	}
	b, _ := json.Marshal(result)
	writeEvent("result", string(b))
	writeEvent("done", `{"success":true}`)
	return nil
}

type sseWriter struct {
	emit func(event, data string)
}

func (w *sseWriter) Write(p []byte) (int, error) {
	w.emit("log", string(p))
	return len(p), nil
}
