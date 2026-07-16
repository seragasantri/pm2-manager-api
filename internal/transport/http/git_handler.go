package httpx

import (
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

// pullRequest is the body for POST /api/git/pull.
type pullRequest struct {
	AppName string `json:"appName"`
	Branch  string `json:"branch"`
}

// Pull POST /api/git/pull
// Streams git output via Server-Sent Events (text/event-stream) so the FE
// can render the log as it runs, then a final `result` event with the
// PullResult JSON.
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
	c.Response().WriteHeader(200)
	flusher, _ := c.Response().Writer.(interface {
		Flush() error
	})

	writeEvent := func(event, data string) {
		_, _ = c.Response().Write([]byte("event: " + event + "\n"))
		_, _ = c.Response().Write([]byte("data: " + data + "\n\n"))
		if flusher != nil {
			_ = flusher.Flush()
		}
	}

	// Use a buffered writer that prepends each chunk as a `log` event.
	sw := &sseWriter{emit: writeEvent}

	result, err := h.svc.Pull(c.Request().Context(), req.AppName, req.Branch, ClaimsFrom(c), sw)
	if err != nil {
		writeEvent("error", err.Error())
		return nil
	}
	writeEvent("result", mustJSON(result))
	writeEvent("done", "")
	return nil
}

// sseWriter wraps the SSE emitter as an io.Writer so git's stdout/stderr are
// streamed line-by-line.
type sseWriter struct {
	emit func(event, data string)
}

func (w *sseWriter) Write(p []byte) (int, error) {
	// Emit as a single log chunk; FE can split on \n if needed.
	w.emit("log", string(p))
	return len(p), nil
}

func mustJSON(v any) string {
	// Tiny helper to avoid pulling encoding/json for a single call.
	b, err := jsonMarshal(v)
	if err != nil {
		return `{"error":"json marshal"}`
	}
	return string(b)
}

func jsonMarshal(v any) ([]byte, error) {
	// Wrapped to keep imports tidy in this file.
	return jsonMarshalImpl(v)
}
