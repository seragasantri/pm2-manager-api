package httpx

import (
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type FileHandler struct {
	svc *service.FileService
}

func NewFileHandler(s *service.FileService) *FileHandler { return &FileHandler{svc: s} }

func (h *FileHandler) checkAccess(c echo.Context, appName string) error {
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

func (h *FileHandler) List(c echo.Context) error {
	appName := strings.TrimSpace(c.QueryParam("appName"))
	dir := c.QueryParam("dir")
	if appName == "" {
		return response.ValidationError(c, map[string]string{"appName": "appName diperlukan"})
	}
	if err := h.checkAccess(c, appName); err != nil {
		return err
	}
	res, err := h.svc.List(c.Request().Context(), appName, dir, ClaimsFrom(c))
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, res)
}

type filePathRequest struct {
	AppName  string `json:"appName"`
	FilePath string `json:"filePath"`
	DirPath  string `json:"dirPath"`
	Content  string `json:"content"`
}

func (h *FileHandler) Read(c echo.Context) error {
	var req filePathRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if req.AppName == "" || req.FilePath == "" {
		return response.ValidationError(c, map[string]string{
			"appName":  "appName diperlukan",
			"filePath": "filePath diperlukan",
		})
	}
	if err := h.checkAccess(c, req.AppName); err != nil {
		return err
	}
	content, err := h.svc.Read(c.Request().Context(), req.AppName, req.FilePath)
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, map[string]string{"content": content})
}

func (h *FileHandler) Write(c echo.Context) error {
	var req filePathRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if req.AppName == "" || req.FilePath == "" {
		return response.ValidationError(c, map[string]string{
			"appName":  "appName diperlukan",
			"filePath": "filePath diperlukan",
		})
	}
	if err := h.checkAccess(c, req.AppName); err != nil {
		return err
	}
	if err := h.svc.Write(c.Request().Context(), req.AppName, req.FilePath, req.Content); err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, nil, "File berhasil disimpan")
}

func (h *FileHandler) Delete(c echo.Context) error {
	var req filePathRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if req.AppName == "" || req.FilePath == "" {
		return response.ValidationError(c, map[string]string{
			"appName":  "appName diperlukan",
			"filePath": "filePath diperlukan",
		})
	}
	if err := h.checkAccess(c, req.AppName); err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), req.AppName, req.FilePath); err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, nil, "Berhasil dihapus")
}

func (h *FileHandler) CreateDir(c echo.Context) error {
	var req filePathRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if req.AppName == "" || req.DirPath == "" {
		return response.ValidationError(c, map[string]string{
			"appName": "appName diperlukan",
			"dirPath": "dirPath diperlukan",
		})
	}
	if err := h.checkAccess(c, req.AppName); err != nil {
		return err
	}
	if err := h.svc.CreateDir(c.Request().Context(), req.AppName, req.DirPath); err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, nil, "Folder berhasil dibuat")
}
