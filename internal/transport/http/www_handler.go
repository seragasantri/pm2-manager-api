package httpx

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/docker"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

// WWWHandler — Websites & File Manager untuk /var/www (khusus superadmin).
type WWWHandler struct {
	svc       *service.WWWService
	jwt       *auth.Service
	pm2Cli    *pm2.Client
	dockerCli *docker.Client
}

func NewWWWHandler(s *service.WWWService, j *auth.Service, p *pm2.Client, d *docker.Client) *WWWHandler {
	return &WWWHandler{svc: s, jwt: j, pm2Cli: p, dockerCli: d}
}

func wwwError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrFileAccessDenied):
		return response.Forbidden(c, "akses ditolak: path di luar "+"root yang diizinkan")
	case errors.Is(err, service.ErrWWWNotFound), errors.Is(err, os.ErrNotExist):
		return response.NotFound(c, service.ErrWWWNotFound.Error())
	case errors.Is(err, service.ErrWWWExists), errors.Is(err, service.ErrWWWConflict):
		return response.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrWWWBadName), errors.Is(err, service.ErrWWWBadMode),
		errors.Is(err, service.ErrWWWUnsupport), errors.Is(err, service.ErrWWWBinary),
		errors.Is(err, service.ErrWWWTooLarge), errors.Is(err, service.ErrWWWIsDir),
		errors.Is(err, service.ErrWWWNotDir), errors.Is(err, service.ErrWWWRoot),
		errors.Is(err, service.ErrWWWIntoItself):
		return response.Error(c, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, os.ErrPermission):
		return response.Forbidden(c, "izin sistem ditolak (permission denied) — file dimiliki user lain")
	}
	return response.ServerError(c, err.Error())
}

// runLong menjalankan operasi berat dengan context terlepas dari request
// (tetap selesai walau klien putus). Bila lewat ~80 detik (batas Cloudflare
// 100 detik), balas 202 dan biarkan berjalan di latar belakang.
func runLong(c echo.Context, label string, fn func(ctx context.Context) (any, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	type result struct {
		v   any
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer cancel()
		v, err := fn(ctx)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return wwwError(c, r.err)
		}
		return response.OK(c, r.v, label+" selesai")
	case <-time.After(80 * time.Second):
		return c.JSON(http.StatusAccepted, response.Envelope{
			Success: true,
			Message: label + " masih berjalan di latar belakang — muat ulang folder beberapa saat lagi",
		})
	}
}

func (h *WWWHandler) Sites(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
	defer cancel()
	var pm2Cwds, dockerDirs []string
	if h.pm2Cli != nil {
		if procs, err := h.pm2Cli.List(ctx); err == nil {
			for _, p := range procs {
				if p.Pm2Env.PmCwd != "" {
					pm2Cwds = append(pm2Cwds, filepath.Clean(p.Pm2Env.PmCwd))
				}
			}
		}
	}
	if h.dockerCli != nil {
		if cs, err := h.dockerCli.ListDetailed(ctx); err == nil {
			for _, ct := range cs {
				if ct.ComposeDir != "" {
					dockerDirs = append(dockerDirs, filepath.Clean(ct.ComposeDir))
				}
			}
		}
	}
	sites := h.svc.Sites(ctx, pm2Cwds, dockerDirs)
	return response.OK(c, map[string]any{
		"root":  h.svc.Root(),
		"sites": sites,
		"nginx": h.svc.ParseNginx(),
	})
}

func (h *WWWHandler) List(c echo.Context) error {
	l, err := h.svc.List(c.QueryParam("path"))
	if err != nil {
		return wwwError(c, err)
	}
	return response.OK(c, l)
}

func (h *WWWHandler) Stat(c echo.Context) error {
	e, err := h.svc.Stat(c.QueryParam("path"))
	if err != nil {
		return wwwError(c, err)
	}
	return response.OK(c, e)
}

func (h *WWWHandler) Read(c echo.Context) error {
	f, err := h.svc.Read(c.QueryParam("path"))
	if err != nil {
		return wwwError(c, err)
	}
	return response.OK(c, f)
}

type wwwWriteReq struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mtime   int64  `json:"mtime"`
}

func (h *WWWHandler) Write(c echo.Context) error {
	var req wwwWriteReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return response.ValidationError(c, "path & content diperlukan")
	}
	f, err := h.svc.Write(req.Path, req.Content, req.Mtime)
	if err != nil {
		return wwwError(c, err)
	}
	auditRecord(c, "www.write", "file", req.Path, map[string]any{"size": f.Size})
	return response.OK(c, f, "File disimpan")
}

type wwwCreateReq struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Type string `json:"type"` // file | dir
}

func (h *WWWHandler) Create(c echo.Context) error {
	var req wwwCreateReq
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	e, err := h.svc.Create(req.Dir, strings.TrimSpace(req.Name), req.Type == "dir")
	if err != nil {
		return wwwError(c, err)
	}
	auditRecord(c, "www.create", req.Type, e.Path, nil)
	return response.Created(c, e, "Berhasil dibuat")
}

type wwwRenameReq struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

func (h *WWWHandler) Rename(c echo.Context) error {
	var req wwwRenameReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return response.ValidationError(c, "path & name diperlukan")
	}
	np, err := h.svc.Rename(req.Path, strings.TrimSpace(req.Name))
	if err != nil {
		return wwwError(c, err)
	}
	auditRecord(c, "www.rename", "file", req.Path, map[string]any{"to": np})
	return response.OK(c, map[string]string{"path": np}, "Nama diubah")
}

type wwwPathsReq struct {
	Paths     []string `json:"paths"`
	Dest      string   `json:"dest"`
	Mode      string   `json:"mode"`
	Recursive bool     `json:"recursive"`
	Dir       string   `json:"dir"`
	Name      string   `json:"name"`
}

func (h *WWWHandler) Delete(c echo.Context) error {
	var req wwwPathsReq
	if err := c.Bind(&req); err != nil || len(req.Paths) == 0 {
		return response.ValidationError(c, "paths diperlukan")
	}
	n, err := h.svc.Delete(req.Paths)
	if err != nil {
		return wwwError(c, err)
	}
	auditRecord(c, "www.delete", "file", strings.Join(req.Paths, ", "), map[string]any{"count": n})
	return response.OK(c, map[string]int{"count": n}, fmt.Sprintf("%d item dihapus", n))
}

func (h *WWWHandler) Chmod(c echo.Context) error {
	var req wwwPathsReq
	if err := c.Bind(&req); err != nil || len(req.Paths) == 0 {
		return response.ValidationError(c, "paths & mode diperlukan")
	}
	n, err := h.svc.Chmod(req.Paths, req.Mode, req.Recursive)
	if err != nil {
		return wwwError(c, err)
	}
	auditRecord(c, "www.chmod", "file", strings.Join(req.Paths, ", "), map[string]any{"mode": req.Mode, "recursive": req.Recursive})
	return response.OK(c, map[string]int{"count": n}, "Permission diubah")
}

func (h *WWWHandler) Copy(c echo.Context) error {
	var req wwwPathsReq
	if err := c.Bind(&req); err != nil || len(req.Paths) == 0 {
		return response.ValidationError(c, "paths & dest diperlukan")
	}
	auditRecord(c, "www.copy", "file", strings.Join(req.Paths, ", "), map[string]any{"dest": req.Dest})
	return runLong(c, "Salin", func(ctx context.Context) (any, error) {
		n, err := h.svc.Copy(ctx, req.Paths, req.Dest)
		return map[string]int{"count": n}, err
	})
}

func (h *WWWHandler) Move(c echo.Context) error {
	var req wwwPathsReq
	if err := c.Bind(&req); err != nil || len(req.Paths) == 0 {
		return response.ValidationError(c, "paths & dest diperlukan")
	}
	auditRecord(c, "www.move", "file", strings.Join(req.Paths, ", "), map[string]any{"dest": req.Dest})
	return runLong(c, "Pindah", func(ctx context.Context) (any, error) {
		n, err := h.svc.Move(ctx, req.Paths, req.Dest)
		return map[string]int{"count": n}, err
	})
}

func (h *WWWHandler) Compress(c echo.Context) error {
	var req wwwPathsReq
	if err := c.Bind(&req); err != nil || len(req.Paths) == 0 {
		return response.ValidationError(c, "paths, dir & name diperlukan")
	}
	auditRecord(c, "www.compress", "file", strings.Join(req.Paths, ", "), map[string]any{"name": req.Name})
	return runLong(c, "Kompres", func(ctx context.Context) (any, error) {
		return h.svc.Compress(ctx, req.Paths, req.Dir, strings.TrimSpace(req.Name))
	})
}

type wwwExtractReq struct {
	Path      string `json:"path"`
	Dest      string `json:"dest"`
	Overwrite bool   `json:"overwrite"`
}

func (h *WWWHandler) Extract(c echo.Context) error {
	var req wwwExtractReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return response.ValidationError(c, "path & dest diperlukan")
	}
	auditRecord(c, "www.extract", "file", req.Path, map[string]any{"dest": req.Dest})
	return runLong(c, "Ekstrak", func(ctx context.Context) (any, error) {
		n, err := h.svc.Extract(ctx, req.Path, req.Dest, req.Overwrite)
		return map[string]int{"count": n}, err
	})
}

func (h *WWWHandler) Upload(c echo.Context) error {
	path := c.FormValue("path")
	id := c.FormValue("uploadId")
	offset, _ := strconv.ParseInt(c.FormValue("offset"), 10, 64)
	final := c.FormValue("final") == "1" || c.FormValue("final") == "true"
	overwrite := c.FormValue("overwrite") == "1" || c.FormValue("overwrite") == "true"
	if path == "" || id == "" {
		return response.ValidationError(c, "path & uploadId diperlukan")
	}
	file, _, err := c.Request().FormFile("chunk")
	if err != nil {
		return response.ValidationError(c, "potongan file (chunk) diperlukan")
	}
	defer file.Close()
	e, err := h.svc.UploadChunk(path, id, offset, file, final, overwrite)
	if err != nil {
		if offset == 0 || !errors.Is(err, service.ErrWWWExists) {
			h.svc.AbortUpload(path, id)
		}
		return wwwError(c, err)
	}
	if final {
		auditRecord(c, "www.upload", "file", path, map[string]any{"size": e.Size})
		return response.OK(c, e, "Upload selesai")
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *WWWHandler) AbortUpload(c echo.Context) error {
	var req struct {
		Path     string `json:"path"`
		UploadID string `json:"uploadId"`
	}
	_ = c.Bind(&req)
	h.svc.AbortUpload(req.Path, req.UploadID)
	return c.NoContent(http.StatusNoContent)
}

func (h *WWWHandler) Search(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 12*time.Second)
	defer cancel()
	res, truncated, err := h.svc.Search(ctx, c.QueryParam("path"), c.QueryParam("q"), 300)
	if err != nil {
		return wwwError(c, err)
	}
	return response.OK(c, map[string]any{"results": res, "truncated": truncated})
}

func (h *WWWHandler) DiskUsage(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 20*time.Second)
	defer cancel()
	size, files, partial, err := h.svc.DiskUsage(ctx, c.QueryParam("path"))
	if err != nil {
		return wwwError(c, err)
	}
	return response.OK(c, map[string]any{"bytes": size, "files": files, "partial": partial})
}

type wwwTicketReq struct {
	Path   string `json:"path"`
	Inline bool   `json:"inline"`
}

// Ticket membuat URL download/preview berumur 2 menit (tanpa JWT sesi di URL).
func (h *WWWHandler) Ticket(c echo.Context) error {
	var req wwwTicketReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return response.ValidationError(c, "path diperlukan")
	}
	if _, _, err := h.svc.OpenForDownload(req.Path); err != nil {
		return wwwError(c, err)
	}
	t, err := h.jwt.IssueTicket("www-dl", req.Path, req.Inline, 2*time.Minute)
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	if !req.Inline {
		auditRecord(c, "www.download", "file", req.Path, nil)
	}
	return response.OK(c, map[string]string{"ticket": t, "query": "ticket=" + url.QueryEscape(t)})
}

// Download melayani file (atau folder sebagai .zip). Tanpa middleware JWT —
// otorisasi lewat tiket.
func (h *WWWHandler) Download(c echo.Context) error {
	tc, err := h.jwt.VerifyTicket(c.QueryParam("ticket"), "www-dl")
	if err != nil {
		return response.Unauthorized(c, "tiket download tidak valid atau kedaluwarsa")
	}
	abs, info, err := h.svc.OpenForDownload(tc.Subject)
	if err != nil {
		return wwwError(c, err)
	}
	name := filepath.Base(abs)
	if info.IsDir() {
		c.Response().Header().Set("Content-Type", "application/zip")
		c.Response().Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"}))
		c.Response().WriteHeader(http.StatusOK)
		return h.svc.ZipTo(c.Request().Context(), c.Response(), []string{abs})
	}
	disp := "attachment"
	if ct, ok := inlineTypes[strings.ToLower(filepath.Ext(name))]; tc.Inline && ok {
		// Preview hanya untuk tipe media aman; HTML/JS/SVG dll tetap diunduh.
		disp = "inline"
		c.Response().Header().Set("Content-Type", ct)
		if ct != "application/pdf" { // viewer PDF bawaan Chrome menolak CSP sandbox
			c.Response().Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; sandbox")
		}
		c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	}
	c.Response().Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
	f, err := os.Open(abs)
	if err != nil {
		return wwwError(c, err)
	}
	defer f.Close()
	http.ServeContent(c.Response(), c.Request(), name, info.ModTime(), f)
	return nil
}

var inlineTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".pdf": "application/pdf", ".mp4": "video/mp4", ".webm": "video/webm",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
}
