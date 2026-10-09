package repository

import (
	"context"

	"github.com/tragasolusi/pm2-manager-api/internal/database"
)

// AiConfig adalah konfigurasi provider AI aktif (singleton, id=1).
// API key disimpan utuh di DB — TIDAK PERNAH dikirim utuh ke client
// (handler hanya mengembalikan versi mask).
type AiConfig struct {
	BaseURL string `json:"base_url"`
	ApiKey  string `json:"api_key"`
	Model   string `json:"model"`
}

type AiConfigRepository struct {
	db *database.DB
}

func NewAiConfigRepository(db *database.DB) *AiConfigRepository {
	return &AiConfigRepository{db: db}
}

// EnsureTable membuat tabel ai_config bila belum ada.
func (r *AiConfigRepository) EnsureTable(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS ai_config (
  id INT PRIMARY KEY DEFAULT 1,
  base_url VARCHAR(500) NOT NULL DEFAULT '',
  api_key TEXT NOT NULL,
  model VARCHAR(255) NOT NULL DEFAULT '',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT chk_ai_config_singleton CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`)
	return err
}

func (r *AiConfigRepository) Get(ctx context.Context) (*AiConfig, error) {
	row := r.db.QueryRow(ctx, `SELECT base_url, api_key, model FROM ai_config WHERE id = 1`)
	var c AiConfig
	if err := row.Scan(&c.BaseURL, &c.ApiKey, &c.Model); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *AiConfigRepository) Save(ctx context.Context, c *AiConfig) error {
	_, err := r.db.Exec(ctx, `
INSERT INTO ai_config (id, base_url, api_key, model) VALUES (1, ?, ?, ?)
ON DUPLICATE KEY UPDATE base_url = VALUES(base_url), api_key = VALUES(api_key), model = VALUES(model)`,
		c.BaseURL, c.ApiKey, c.Model)
	return err
}
