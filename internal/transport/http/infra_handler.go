package httpx

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/docker"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

// InfraHandler — halaman Docker, PM2 & informasi server (khusus superadmin).
type InfraHandler struct {
	dockerCli *docker.Client
	pm2Cli    *pm2.Client
	pm2Bin    string
	sys       *service.SystemService
}

func NewInfraHandler(d *docker.Client, p *pm2.Client, pm2Bin string, sys *service.SystemService) *InfraHandler {
	return &InfraHandler{dockerCli: d, pm2Cli: p, pm2Bin: pm2Bin, sys: sys}
}

// ─── Docker ────────────────────────────────────────────────────────────────

func (h *InfraHandler) requireDocker(c echo.Context) bool {
	if h.dockerCli == nil {
		_ = response.Error(c, http.StatusServiceUnavailable, "Docker tidak tersedia di server ini")
		return false
	}
	return true
}

// DockerOverview: container (+cpu/mem), images & info engine.
func (h *InfraHandler) DockerOverview(c echo.Context) error {
	if !h.requireDocker(c) {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 12*time.Second)
	defer cancel()

	containers, err := h.dockerCli.ListDetailed(ctx)
	if err != nil {
		return response.ServerError(c, "docker: "+err.Error())
	}

	// Statistik live untuk container yang berjalan (paralel, anggaran 4 detik).
	statCtx, statCancel := context.WithTimeout(ctx, 4*time.Second)
	var wg sync.WaitGroup
	for i := range containers {
		if containers[i].State != "running" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if st, err := h.dockerCli.Stats(statCtx, containers[i].ID); err == nil && st != nil {
				containers[i].CPU = st.CPU
				containers[i].Memory = st.Memory
				containers[i].MemLimit = st.MemLim
			}
		}(i)
	}
	wg.Wait()
	statCancel()

	images, _ := h.dockerCli.Images(ctx)
	info, _ := h.dockerCli.EngineInfo(ctx)
	return response.OK(c, map[string]any{
		"containers": containers,
		"images":     images,
		"engine":     info,
	})
}

func (h *InfraHandler) DockerLogs(c echo.Context) error {
	if !h.requireDocker(c) {
		return nil
	}
	tail, _ := strconv.Atoi(c.QueryParam("tail"))
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
	defer cancel()
	logs, err := h.dockerCli.Logs(ctx, c.Param("id"), tail, c.QueryParam("timestamps") == "1")
	if err != nil && logs == "" {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, map[string]string{"logs": logs})
}

func (h *InfraHandler) DockerInspect(c echo.Context) error {
	if !h.requireDocker(c) {
		return nil
	}
	v, err := h.dockerCli.InspectView(c.Request().Context(), c.Param("id"))
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, v)
}

func (h *InfraHandler) DockerAction(c echo.Context) error {
	if !h.requireDocker(c) {
		return nil
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := c.Bind(&req); err != nil || req.Action == "" {
		return response.ValidationError(c, "action diperlukan")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
	defer cancel()
	id := c.Param("id")
	if err := h.dockerCli.ExtAction(ctx, id, req.Action); err != nil {
		return response.ServerError(c, err.Error())
	}
	auditRecord(c, "docker."+req.Action, "container", id, nil)
	return response.OK(c, nil, "Container berhasil di-"+req.Action)
}

// ─── PM2 ───────────────────────────────────────────────────────────────────

func (h *InfraHandler) Pm2List(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
	defer cancel()
	procs, err := h.pm2Cli.ListDetailed(ctx)
	if err != nil {
		return response.ServerError(c, "pm2: "+err.Error())
	}
	return response.OK(c, procs)
}

func (h *InfraHandler) Pm2Logs(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
	defer cancel()
	p, err := h.pm2Cli.Find(ctx, c.Param("name"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	lines, _ := strconv.Atoi(c.QueryParam("lines"))
	if lines <= 0 || lines > 5000 {
		lines = 300
	}
	out, outErr := pm2.TailFile(p.OutLog, lines)
	errLog, errErr := pm2.TailFile(p.ErrLog, lines)
	res := map[string]any{"out": out, "err": errLog, "outPath": p.OutLog, "errPath": p.ErrLog}
	if outErr != nil {
		res["outError"] = outErr.Error()
	}
	if errErr != nil {
		res["errError"] = errErr.Error()
	}
	return response.OK(c, res)
}

func (h *InfraHandler) Pm2Action(c echo.Context) error {
	var req struct {
		Action string `json:"action"`
	}
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "action diperlukan")
	}
	allowed := map[string]bool{"start": true, "stop": true, "restart": true, "reload": true, "flush": true, "reset": true}
	if !allowed[req.Action] {
		return response.ValidationError(c, "action harus start, stop, restart, reload, flush, atau reset")
	}
	name := c.Param("name")
	ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
	defer cancel()
	if _, err := h.pm2Cli.Find(ctx, name); err != nil {
		return response.NotFound(c, err.Error())
	}
	out, err := h.pm2Cli.Run(ctx, req.Action, name)
	if err != nil {
		return response.ServerError(c, strings.TrimSpace(err.Error()))
	}
	auditRecord(c, "pm2."+req.Action, "process", name, nil)
	return response.OK(c, map[string]string{"output": out}, "Proses '"+name+"' berhasil di-"+req.Action)
}

func (h *InfraHandler) Pm2Save(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
	defer cancel()
	out, err := h.pm2Cli.Run(ctx, "save")
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	auditRecord(c, "pm2.save", "process", "*", nil)
	return response.OK(c, map[string]string{"output": out}, "Daftar proses PM2 disimpan (pm2 save)")
}

// ─── Sistem ────────────────────────────────────────────────────────────────

func (h *InfraHandler) SystemOverview(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
	defer cancel()
	return response.OK(c, h.sys.Overview(ctx, h.pm2Bin))
}
