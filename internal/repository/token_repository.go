package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/tragasolusi/pm2-manager-api/internal/database"
	"github.com/tragasolusi/pm2-manager-api/internal/model"
)

type TokenRepository struct {
	db *database.DB
}

func NewTokenRepository(db *database.DB) *TokenRepository {
	return &TokenRepository{db: db}
}

func (r *TokenRepository) FindByCode(ctx context.Context, code string) (*model.Token, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, code, label, allowed_apps, IFNULL(allowed_actions,'[]'), created_by, created_at, updated_at
		   FROM tokens WHERE code = ?`, code)
	return scanTokenWithActions(row)
}

func (r *TokenRepository) FindByID(ctx context.Context, id int64) (*model.Token, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, code, label, allowed_apps, IFNULL(allowed_actions,'[]'), created_by, created_at, updated_at
		   FROM tokens WHERE id = ?`, id)
	return scanTokenWithActions(row)
}

func (r *TokenRepository) All(ctx context.Context) ([]*model.Token, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, code, label, allowed_apps, IFNULL(allowed_actions,'[]'), created_by, created_at, updated_at
		   FROM tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Token
	for rows.Next() {
		var t model.Token
		var appsJSON, actionsJSON []byte
		var createdBy sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Code, &t.Label, &appsJSON, &actionsJSON, &createdBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if err := unmarshalApps(appsJSON, &t.AllowedApps); err != nil {
			return nil, err
		}
		_ = unmarshalApps(actionsJSON, &t.AllowedActions)
		if createdBy.Valid {
			v := createdBy.Int64
			t.CreatedBy = &v
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (r *TokenRepository) Create(ctx context.Context, label string, allowedApps []string, createdBy *int64) (*model.Token, error) {
	code, err := randomCode(16)
	if err != nil {
		return nil, err
	}
	appsJSON, err := json.Marshal(allowedApps)
	if err != nil {
		return nil, err
	}
	actionsJSON, _ := json.Marshal([]string{})

	res, err := r.db.Exec(ctx,
		`INSERT INTO tokens (code, label, allowed_apps, allowed_actions, created_by, created_at, updated_at)
		      VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		code, label, string(appsJSON), string(actionsJSON), nullableInt(createdBy),
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.Token{
		ID:          id,
		Code:        code,
		Label:       label,
		AllowedApps: allowedApps,
		CreatedBy:   createdBy,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

func (r *TokenRepository) Update(ctx context.Context, id int64, label string, allowedApps []string) error {
	appsJSON, err := json.Marshal(allowedApps)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx,
		`UPDATE tokens SET label = ?, allowed_apps = ?, updated_at = NOW() WHERE id = ?`,
		label, string(appsJSON), id,
	)
	return err
}

func (r *TokenRepository) UpdateWithActions(ctx context.Context, id int64, label string, allowedApps, allowedActions []string) error {
	appsJSON, err := json.Marshal(allowedApps)
	if err != nil {
		return err
	}
	actionsJSON, _ := json.Marshal(allowedActions)
	_, err = r.db.Exec(ctx,
		`UPDATE tokens SET label = ?, allowed_apps = ?, allowed_actions = ?, updated_at = NOW() WHERE id = ?`,
		label, string(appsJSON), string(actionsJSON), id,
	)
	return err
}

func (r *TokenRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, `DELETE FROM tokens WHERE id = ?`, id)
	return err
}

func scanToken(row *sql.Row) (*model.Token, error) {
	var t model.Token
	var appsJSON []byte
	var createdBy sql.NullInt64

	err := row.Scan(&t.ID, &t.Code, &t.Label, &appsJSON, &createdBy, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := unmarshalApps(appsJSON, &t.AllowedApps); err != nil {
		return nil, err
	}
	if createdBy.Valid {
		v := createdBy.Int64
		t.CreatedBy = &v
	}
	return &t, nil
}

func scanTokenWithActions(row *sql.Row) (*model.Token, error) {
	var t model.Token
	var appsJSON, actionsJSON []byte
	var createdBy sql.NullInt64
	err := row.Scan(&t.ID, &t.Code, &t.Label, &appsJSON, &actionsJSON, &createdBy, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		// column allowed_actions may not exist yet — fallback to old scan (apps only) by re-querying without it
		return nil, err
	}
	_ = unmarshalApps(appsJSON, &t.AllowedApps)
	_ = unmarshalApps(actionsJSON, &t.AllowedActions)
	if createdBy.Valid {
		v := createdBy.Int64
		t.CreatedBy = &v
	}
	return &t, nil
}

func unmarshalApps(raw []byte, out *[]string) error {
	if len(raw) == 0 {
		*out = []string{}
		return nil
	}
	return json.Unmarshal(raw, out)
}

func randomCode(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func nullableInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
