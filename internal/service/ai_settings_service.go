package service

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/tragasolusi/pm2-manager-api/internal/repository"
)

// AiSettingsService mengelola konfigurasi provider AI (singleton, superadmin).
type AiSettingsService struct {
	repo *repository.AiConfigRepository
}

func NewAiSettingsService(repo *repository.AiConfigRepository) *AiSettingsService {
	return &AiSettingsService{repo: repo}
}

var (
	ErrAiBelumDikonfigurasi = errors.New("provider AI belum dikonfigurasi — simpan pengaturan dulu")
	ErrAiEndpointWajib      = errors.New("endpoint dan API key wajib diisi")
	ErrAiModelWajib         = errors.New("endpoint, API key, dan model wajib diisi")
	ErrAiEndpointInvalid    = errors.New("format endpoint tidak valid")
)

// MaskApiKey menampilkan key tersimpan sebagai mask, mis. "sk-o...9f2a".
func MaskApiKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}
	return key[:4] + "••••••" + key[len(key)-4:]
}

type AiStatus struct {
	Configured  bool   `json:"configured"`
	BaseURL     string `json:"base_url,omitempty"`
	Model       string `json:"model,omitempty"`
	ApiKeyMask  string `json:"api_key_masked,omitempty"`
}

func (s *AiSettingsService) Status(ctx context.Context) (*AiStatus, error) {
	cfg, err := s.repo.Get(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &AiStatus{Configured: false}, nil
		}
		return nil, err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.ApiKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		return &AiStatus{Configured: false}, nil
	}
	return &AiStatus{Configured: true, BaseURL: cfg.BaseURL, Model: cfg.Model, ApiKeyMask: MaskApiKey(cfg.ApiKey)}, nil
}

func (s *AiSettingsService) FetchModels(ctx context.Context, baseURL, apiKey string) ([]AiModelOption, error) {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil, ErrAiEndpointWajib
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return FetchAiModels(ctx, baseURL, apiKey)
}

func (s *AiSettingsService) Save(ctx context.Context, baseURL, apiKey, model string) error {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" || strings.TrimSpace(model) == "" {
		return ErrAiModelWajib
	}
	if _, err := url.ParseRequestURI(strings.TrimSpace(baseURL)); err != nil {
		return ErrAiEndpointInvalid
	}
	return s.repo.Save(ctx, &repository.AiConfig{
		BaseURL: strings.TrimSpace(baseURL),
		ApiKey:  strings.TrimSpace(apiKey),
		Model:   strings.TrimSpace(model),
	})
}

type AiCallResult struct {
	Reply      string `json:"reply"`
	Model      string `json:"model"`
	DurationMs int64  `json:"duration_ms"`
}

func (s *AiSettingsService) configOrFail(ctx context.Context) (*repository.AiConfig, error) {
	cfg, err := s.repo.Get(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAiBelumDikonfigurasi
		}
		return nil, err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.ApiKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, ErrAiBelumDikonfigurasi
	}
	return cfg, nil
}

// CekKoneksi mengirim satu permintaan kecil ke provider tersimpan.
func (s *AiSettingsService) CekKoneksi(ctx context.Context) (*AiCallResult, error) {
	cfg, err := s.configOrFail(ctx)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	reply, err := CallAiChat(ctx, cfg.BaseURL, cfg.ApiKey, cfg.Model,
		[]ChatMessage{{Role: "user", Content: "Reply with exactly: OK"}}, float64Ptr(0.2))
	if err != nil {
		return nil, err
	}
	return &AiCallResult{Reply: reply, Model: cfg.Model, DurationMs: time.Since(started).Milliseconds()}, nil
}

// Playground menjalankan percakapan multi-turn bebas.
func (s *AiSettingsService) Playground(ctx context.Context, history []ChatMessage, systemPrompt string) (*AiCallResult, error) {
	cfg, err := s.configOrFail(ctx)
	if err != nil {
		return nil, err
	}
	turns := make([]ChatMessage, 0, len(history)+1)
	if strings.TrimSpace(systemPrompt) != "" {
		turns = append(turns, ChatMessage{Role: "system", Content: strings.TrimSpace(systemPrompt)})
	}
	for _, m := range history {
		if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Content) != "" {
			turns = append(turns, m)
		}
	}
	if len(turns) == 0 || turns[len(turns)-1].Role != "user" {
		return nil, errors.New("tulis pesan terlebih dahulu")
	}
	started := time.Now()
	reply, err := CallAiChat(ctx, cfg.BaseURL, cfg.ApiKey, cfg.Model, turns, float64Ptr(0.2))
	if err != nil {
		return nil, err
	}
	return &AiCallResult{Reply: reply, Model: cfg.Model, DurationMs: time.Since(started).Milliseconds()}, nil
}

func float64Ptr(v float64) *float64 { return &v }
