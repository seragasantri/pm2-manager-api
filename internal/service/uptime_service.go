package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TARGETS nginx server_name 11 domain
var Targets = []string{
	"lpm.radenfatah.ac.id",
	"api-lpm.radenfatah.ac.id",
	"sim-obe.radenfatah.ac.id",
	"bari.radenfatah.ac.id",
	"pkdp.radenfatah.ac.id",
	"syfa.radenfatah.ac.id",
	"api-syfa.radenfatah.ac.id",
	"fushpiapp.radenfatah.ac.id",
	"homebase.radenfatah.ac.id",
	"inn.radenfatah.ac.id",
	"koperasiapp.radenfatah.ac.id",
}

var ErrTargetNotFound = errors.New("domain tidak ditemukan di daftar target")

type UptimeTarget struct {
	Domain       string    `json:"domain"`
	URL          string    `json:"url"`
	Status       string    `json:"status"` // up | down
	LatencyMs    int64     `json:"latencyMs"`
	StatusCode   int       `json:"statusCode"`
	CertExpiry   time.Time `json:"certExpiry"`
	CertDaysLeft int       `json:"certDaysLeft"`
	LastChecked  time.Time `json:"lastChecked"`
	Error        string    `json:"error,omitempty"`
}

type cacheEntry struct {
	data UptimeTarget
	at   time.Time
}

type UptimeService struct {
	mu     sync.RWMutex
	cache  map[string]cacheEntry
	client *http.Client
}

func NewUptimeService() *UptimeService {
	return &UptimeService{
		cache:  make(map[string]cacheEntry),
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

const cacheTTL = 30 * time.Second

// fetchCertExpiry dials TLS 443 and returns leaf cert NotAfter + days left.
func fetchCertExpiry(domain string) (time.Time, int, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(domain, "443"), &tls.Config{
		ServerName: domain,
	})
	if err != nil {
		return time.Time{}, 0, err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return time.Time{}, 0, errors.New("no peer certificate")
	}
	exp := certs[0].NotAfter
	days := int(time.Until(exp).Hours() / 24)
	return exp, days, nil
}

func (s *UptimeService) checkSingle(ctx context.Context, domain string) UptimeTarget {
	t := UptimeTarget{
		Domain:      domain,
		URL:         "https://" + domain,
		Status:      "down",
		LastChecked: time.Now(),
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		t.Error = err.Error()
	} else {
		req.Header.Set("User-Agent", "PanelPM-Uptime/1.0")
		resp, err := s.client.Do(req)
		t.LatencyMs = time.Since(start).Milliseconds()
		if err != nil {
			t.Error = err.Error()
		} else {
			defer resp.Body.Close()
			t.StatusCode = resp.StatusCode
			if resp.StatusCode < 400 {
				t.Status = "up"
			} else {
				t.Status = "down"
				t.Error = http.StatusText(resp.StatusCode)
			}
		}
	}
	// cert check best-effort; don't overwrite http error if already set unless cert also fails and no http error
	if exp, days, err := fetchCertExpiry(domain); err == nil {
		t.CertExpiry = exp
		t.CertDaysLeft = days
	}
	return t
}

func isAllowed(domain string) bool {
	for _, d := range Targets {
		if d == domain {
			return true
		}
	}
	return false
}

// CheckAll parallel GET https://domain timeout 5s, follow redirect (default client), TTL 30s cache.
func (s *UptimeService) CheckAll(ctx context.Context) ([]UptimeTarget, error) {
	// fast path: all cached
	s.mu.RLock()
	allCached := true
	cached := make([]UptimeTarget, 0, len(Targets))
	for _, d := range Targets {
		e, ok := s.cache[d]
		if !ok || time.Since(e.at) >= cacheTTL {
			allCached = false
			break
		}
		cached = append(cached, e.data)
	}
	if allCached {
		s.mu.RUnlock()
		return cached, nil
	}
	s.mu.RUnlock()

	results := make([]UptimeTarget, len(Targets))
	var wg sync.WaitGroup
	for i, domain := range Targets {
		s.mu.RLock()
		e, ok := s.cache[domain]
		hit := ok && time.Since(e.at) < cacheTTL
		s.mu.RUnlock()
		if hit {
			results[i] = e.data
			continue
		}
		wg.Add(1)
		go func(idx int, d string) {
			defer wg.Done()
			r := s.checkSingle(ctx, d)
			s.mu.Lock()
			s.cache[d] = cacheEntry{data: r, at: time.Now()}
			s.mu.Unlock()
			results[idx] = r
		}(i, domain)
	}
	wg.Wait()
	// fill any gaps that were cached (already filled above) — nothing else needed
	return results, nil
}

// CheckOne checks single domain with same TTL cache.
func (s *UptimeService) CheckOne(ctx context.Context, domain string) (*UptimeTarget, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, errors.New("domain diperlukan")
	}
	if !isAllowed(domain) {
		return nil, ErrTargetNotFound
	}
	s.mu.RLock()
	if e, ok := s.cache[domain]; ok && time.Since(e.at) < cacheTTL {
		cp := e.data
		s.mu.RUnlock()
		return &cp, nil
	}
	s.mu.RUnlock()

	r := s.checkSingle(ctx, domain)
	s.mu.Lock()
	s.cache[domain] = cacheEntry{data: r, at: time.Now()}
	s.mu.Unlock()
	return &r, nil
}

// ponytail: TTL 30s in-memory only. Upgrade to redis/distributed cache when multi-instance.
