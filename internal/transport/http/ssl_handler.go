package httpx

import (
	"errors"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type SSLHandler struct {
	svc *service.SSLService
}

func NewSSLHandler(s *service.SSLService) *SSLHandler { return &SSLHandler{svc: s} }

// Index GET /ssl/certificates
func (h *SSLHandler) Index(c echo.Context) error {
	certs, err := h.svc.GetCertificates()
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, certs)
}

// Show GET /ssl/certificates/:id
func (h *SSLHandler) Show(c echo.Context) error {
	id := c.Param("id")
	if strings.TrimSpace(id) == "" {
		return response.ValidationError(c, "id diperlukan")
	}
	cert, err := h.svc.GetCertificate(id)
	if err != nil {
		if errors.Is(err, service.ErrCertNotFound) {
			return response.NotFound(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, cert)
}

type uploadCertRequest struct {
	Domain   string `json:"domain"`
	Cert     string `json:"cert"`
	Key      string `json:"key"`
	CABundle string `json:"caBundle"`
}

// Store POST /ssl/certificates
func (h *SSLHandler) Store(c echo.Context) error {
	var req uploadCertRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	cert, err := h.svc.UploadCertificate(req.Domain, req.Cert, req.Key, req.CABundle)
	if err != nil {
		if errors.Is(err, service.ErrDomainRequired) || errors.Is(err, service.ErrCertRequired) || errors.Is(err, service.ErrKeyRequired) || errors.Is(err, service.ErrInvalidDomain) {
			return response.ValidationError(c, err.Error())
		}
		// validation-like errors from PEM parsing
		if strings.Contains(err.Error(), "format") || strings.Contains(err.Error(), "tidak valid") {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.Created(c, cert, "Sertifikat berhasil diupload")
}

// Destroy DELETE /ssl/certificates/:id
func (h *SSLHandler) Destroy(c echo.Context) error {
	id := c.Param("id")
	if strings.TrimSpace(id) == "" {
		return response.ValidationError(c, "id diperlukan")
	}
	if err := h.svc.DeleteCertificate(id); err != nil {
		if errors.Is(err, service.ErrCertNotFound) {
			return response.NotFound(c, err.Error())
		}
		if strings.Contains(err.Error(), "letsencrypt") {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, nil, "Sertifikat berhasil dihapus")
}

type letsEncryptRequest struct {
	Domain string `json:"domain"`
	Email  string `json:"email"`
}

// LetsEncrypt POST /ssl/letsencrypt
func (h *SSLHandler) LetsEncrypt(c echo.Context) error {
	var req letsEncryptRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if strings.TrimSpace(req.Domain) == "" {
		return response.ValidationError(c, map[string]string{"domain": "domain diperlukan"})
	}
	res, err := h.svc.RequestLetsEncrypt(req.Domain, req.Email)
	if err != nil {
		if errors.Is(err, service.ErrDomainRequired) {
			return response.ValidationError(c, err.Error())
		}
		if strings.Contains(err.Error(), "email") {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, res)
}
