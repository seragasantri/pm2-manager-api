package service

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	ErrCertNotFound   = errors.New("sertifikat tidak ditemukan")
	ErrDomainRequired = errors.New("domain diperlukan")
	ErrCertRequired   = errors.New("certificate diperlukan")
	ErrKeyRequired    = errors.New("private key diperlukan")
	ErrInvalidDomain  = errors.New("domain tidak valid")
)

var domainRe = regexp.MustCompile(`^(\*\.)?[a-zA-Z0-9][a-zA-Z0-9\-\.]*\.[a-zA-Z]{2,}$`)

const (
	nginxSSLDir     = "/etc/nginx/ssl"
	letsEncryptLive = "/etc/letsencrypt/live"
)

// Cert represents an SSL certificate.
type Cert struct {
	ID        string    `json:"id"`
	Domain    string    `json:"domain"`
	Type      string    `json:"type"` // "custom" | "letsencrypt"
	Issuer    string    `json:"issuer"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Path      string    `json:"path,omitempty"`
}

// SSLService manages SSL certificates on disk.
type SSLService struct{}

func NewSSLService() *SSLService { return &SSLService{} }

func parseCertFile(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			cert, err := x509.ParseCertificate(block.Bytes)
			if err == nil {
				return cert, nil
			}
		}
		if len(data) == 0 {
			break
		}
	}
	return nil, fmt.Errorf("no valid certificate in %s", path)
}

func certFromX509(id, domain, typ, path string, xc *x509.Certificate) Cert {
	issuer := ""
	if xc.Issuer.CommonName != "" {
		issuer = xc.Issuer.CommonName
	} else if len(xc.Issuer.Organization) > 0 {
		issuer = xc.Issuer.Organization[0]
	}
	return Cert{
		ID:        id,
		Domain:    domain,
		Type:      typ,
		Issuer:    issuer,
		IssuedAt:  xc.NotBefore,
		ExpiresAt: xc.NotAfter,
		Path:      path,
	}
}

// GetCertificates scans nginx ssl and letsencrypt dirs.
func (s *SSLService) GetCertificates() ([]Cert, error) {
	var certs []Cert

	// 1. /etc/nginx/ssl/*.crt
	if entries, err := os.ReadDir(nginxSSLDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if !strings.HasSuffix(e.Name(), ".crt") && !strings.HasSuffix(e.Name(), ".pem") {
				continue
			}
			domain := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".crt"), ".pem")
			fullPath := filepath.Join(nginxSSLDir, e.Name())
			xc, err := parseCertFile(fullPath)
			if err != nil {
				// include with empty dates if unparseable
				certs = append(certs, Cert{ID: domain, Domain: domain, Type: "custom", Path: fullPath})
				continue
			}
			certs = append(certs, certFromX509(domain, domain, "custom", fullPath, xc))
		}
	}

	// 2. /etc/letsencrypt/live/*/cert.pem
	if entries, err := os.ReadDir(letsEncryptLive); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			domain := e.Name()
			certPath := filepath.Join(letsEncryptLive, domain, "cert.pem")
			xc, err := parseCertFile(certPath)
			if err != nil {
				continue
			}
			certs = append(certs, certFromX509(domain, domain, "letsencrypt", certPath, xc))
		}
	}

	if certs == nil {
		certs = []Cert{}
	}
	return certs, nil
}

// GetCertificate finds cert by id (domain).
func (s *SSLService) GetCertificate(id string) (*Cert, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrCertNotFound
	}
	certs, err := s.GetCertificates()
	if err != nil {
		return nil, err
	}
	for _, c := range certs {
		if c.ID == id || c.Domain == id {
			cc := c
			return &cc, nil
		}
	}
	return nil, ErrCertNotFound
}

// UploadCertificate writes cert/key to /etc/nginx/ssl/{domain}.crt/.key
func (s *SSLService) UploadCertificate(domain, certPEM, keyPEM, caBundle string) (*Cert, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, ErrDomainRequired
	}
	if !domainRe.MatchString(domain) && domain != "localhost" {
		// allow simple check; still store
		if strings.Contains(domain, " ") || strings.Contains(domain, "/") {
			return nil, ErrInvalidDomain
		}
	}
	certPEM = strings.TrimSpace(certPEM)
	keyPEM = strings.TrimSpace(keyPEM)
	if certPEM == "" {
		return nil, ErrCertRequired
	}
	if keyPEM == "" {
		return nil, ErrKeyRequired
	}

	// validate cert parses
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, errors.New("format certificate tidak valid (bukan PEM)")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return nil, fmt.Errorf("certificate tidak valid: %w", err)
	}
	// validate key is PEM
	kb, _ := pem.Decode([]byte(keyPEM))
	if kb == nil {
		return nil, errors.New("format private key tidak valid (bukan PEM)")
	}

	if err := os.MkdirAll(nginxSSLDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir ssl dir: %w", err)
	}

	crtPath := filepath.Join(nginxSSLDir, domain+".crt")
	keyPath := filepath.Join(nginxSSLDir, domain+".key")

	// optionally append caBundle to crt
	fullCert := certPEM
	if cb := strings.TrimSpace(caBundle); cb != "" {
		fullCert = strings.TrimRight(certPEM, "\n") + "\n" + strings.TrimSpace(cb) + "\n"
	}

	if err := os.WriteFile(crtPath, []byte(fullCert), 0644); err != nil {
		return nil, fmt.Errorf("write crt: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(keyPEM), 0600); err != nil {
		return nil, fmt.Errorf("write key: %w", err)
	}

	// return parsed cert
	xc, err := parseCertFile(crtPath)
	if err != nil {
		return &Cert{ID: domain, Domain: domain, Type: "custom", Path: crtPath}, nil
	}
	c := certFromX509(domain, domain, "custom", crtPath, xc)
	return &c, nil
}

// DeleteCertificate removes cert and key files.
func (s *SSLService) DeleteCertificate(id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrCertNotFound
	}
	cert, err := s.GetCertificate(id)
	if err != nil {
		return err
	}
	// only delete custom nginx certs; letsencrypt managed externally
	if cert.Type == "letsencrypt" {
		return errors.New("sertifikat letsencrypt tidak dapat dihapus manual, gunakan certbot delete")
	}
	crtPath := filepath.Join(nginxSSLDir, cert.Domain+".crt")
	keyPath := filepath.Join(nginxSSLDir, cert.Domain+".key")
	// also handle .pem variant
	pemPath := filepath.Join(nginxSSLDir, cert.Domain+".pem")

	// prefer actual path if known
	if cert.Path != "" && strings.HasPrefix(cert.Path, nginxSSLDir) {
		_ = os.Remove(cert.Path)
		// derive key path from cert path
		base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(cert.Path), ".crt"), ".pem")
		_ = os.Remove(filepath.Join(nginxSSLDir, base+".key"))
		return nil
	}

	removed := false
	for _, p := range []string{crtPath, pemPath, keyPath} {
		if err := os.Remove(p); err == nil {
			removed = true
		}
	}
	if !removed {
		return ErrCertNotFound
	}
	return nil
}

// RequestLetsEncrypt is a stub that would invoke certbot. Returns dry-run info.
// ponytail: real certbot exec requires root + nginx plugin. Upgrade to exec.Command("certbot", "certonly", "--nginx", "-d", domain, "--email", email, "--agree-tos", "--dry-run").
func (s *SSLService) RequestLetsEncrypt(domain, email string) (map[string]any, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, ErrDomainRequired
	}
	if email != "" && !strings.Contains(email, "@") {
		return nil, errors.New("email tidak valid")
	}
	// stub: check certbot exists
	certbotExists := false
	if _, err := os.Stat("/usr/bin/certbot"); err == nil {
		certbotExists = true
	}
	if _, err := os.Stat("/snap/bin/certbot"); err == nil {
		certbotExists = true
	}
	return map[string]any{
		"domain":       domain,
		"email":        email,
		"status":       "dry-run",
		"certbotFound": certbotExists,
		"message":      "Stub: jalankan certbot certonly --nginx -d " + domain + " --dry-run untuk validasi. Implementasi real butuh root + nginx plugin.",
		"command":      fmt.Sprintf("certbot certonly --nginx -d %s --email %s --agree-tos --dry-run", domain, email),
	}, nil
}
