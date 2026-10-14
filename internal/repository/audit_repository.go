package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/tragasolusi/pm2-manager-api/internal/database"
)

// AuditLog maps to audit_logs table.
type AuditLog struct {
	ID           int64     `json:"id"`
	UserID       *int64    `json:"user_id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	Label        string    `json:"label"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Detail       string    `json:"detail"`
	IP           string    `json:"ip"`
	UserAgent    string    `json:"user_agent"`
	CreatedAt    time.Time `json:"created_at"`
}

// AuditFilter params for List.
type AuditFilter struct {
	User         string
	Action       string
	ResourceType string
	From         string
	To           string
	Search       string
	Page         int
	Limit        int
}

// AuditRepository persists audit logs.
type AuditRepository struct {
	db *database.DB
}

func NewAuditRepository(db *database.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// EnsureTable creates audit_logs if not exists.
func (r *AuditRepository) EnsureTable(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT,
  username VARCHAR(255),
  role VARCHAR(20),
  label VARCHAR(255),
  action VARCHAR(50),
  resource_type VARCHAR(50),
  resource_id VARCHAR(255),
  detail TEXT,
  ip VARCHAR(45),
  user_agent TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_created_at (created_at),
  INDEX idx_action (action)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`)
	return err
}

// Create inserts one audit log.
func (r *AuditRepository) Create(ctx context.Context, l AuditLog) error {
	var uid any
	if l.UserID != nil {
		uid = *l.UserID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO audit_logs (user_id, username, role, label, action, resource_type, resource_id, detail, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, l.Username, l.Role, l.Label, l.Action, l.ResourceType, l.ResourceID, l.Detail, l.IP, l.UserAgent,
	)
	return err
}

// List returns filtered/paginated logs ordered by id DESC.
func (r *AuditRepository) List(ctx context.Context, f AuditFilter) ([]AuditLog, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Limit < 1 {
		f.Limit = 20
	}
	if f.Limit > 100 {
		f.Limit = 100
	}

	var where []string
	var args []any

	if f.User != "" {
		where = append(where, "(username LIKE ? OR label LIKE ?)")
		like := "%" + f.User + "%"
		args = append(args, like, like)
	}
	if f.Action != "" {
		where = append(where, "action = ?")
		args = append(args, f.Action)
	}
	if f.ResourceType != "" {
		where = append(where, "resource_type = ?")
		args = append(args, f.ResourceType)
	}
	if f.From != "" {
		where = append(where, "created_at >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "created_at <= ?")
		args = append(args, f.To)
	}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		where = append(where, "(username LIKE ? OR label LIKE ? OR action LIKE ? OR resource_type LIKE ? OR resource_id LIKE ? OR detail LIKE ? OR ip LIKE ?)")
		args = append(args, like, like, like, like, like, like, like)
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	// total
	var total int
	countSQL := fmt.Sprintf(`SELECT COUNT(*) FROM audit_logs %s`, whereSQL)
	if err := r.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// data
	offset := (f.Page - 1) * f.Limit
	dataSQL := fmt.Sprintf(`SELECT id, COALESCE(user_id,0), COALESCE(username,''), COALESCE(role,''), COALESCE(label,''), COALESCE(action,''), COALESCE(resource_type,''), COALESCE(resource_id,''), COALESCE(detail,''), COALESCE(ip,''), COALESCE(user_agent,''), created_at
		FROM audit_logs %s ORDER BY id DESC LIMIT ? OFFSET ?`, whereSQL)
	args2 := append(append([]any{}, args...), f.Limit, offset)

	rows, err := r.db.Query(ctx, dataSQL, args2...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []AuditLog
	for rows.Next() {
		var l AuditLog
		var uid int64
		var created sql.NullTime
		if err := rows.Scan(&l.ID, &uid, &l.Username, &l.Role, &l.Label, &l.Action, &l.ResourceType, &l.ResourceID, &l.Detail, &l.IP, &l.UserAgent, &created); err != nil {
			return nil, 0, err
		}
		if uid != 0 {
			l.UserID = &uid
		}
		if created.Valid {
			l.CreatedAt = created.Time
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if out == nil {
		out = []AuditLog{}
	}
	return out, total, nil
}

// Stats returns count per action for last 7 days.
func (r *AuditRepository) Stats(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.Query(ctx, `SELECT action, COUNT(*) FROM audit_logs WHERE created_at >= DATE_SUB(NOW(), INTERVAL 7 DAY) GROUP BY action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var act string
		var cnt int
		if err := rows.Scan(&act, &cnt); err != nil {
			return nil, err
		}
		m[act] = cnt
	}
	if m == nil {
		m = map[string]int{}
	}
	return m, rows.Err()
}
