package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tragasolusi/pm2-manager-api/internal/database"
	"github.com/tragasolusi/pm2-manager-api/internal/model"
)

type AppRepository struct {
	db *database.DB
}

func NewAppRepository(db *database.DB) *AppRepository {
	return &AppRepository{db: db}
}

func (r *AppRepository) FindByName(ctx context.Context, name string) (*model.App, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, name, cwd, pm_id, COALESCE(description,''), created_at, updated_at
		   FROM apps WHERE name = ?`, name)
	return scanApp(row)
}

func (r *AppRepository) All(ctx context.Context) ([]*model.App, error) {
	return r.query(ctx, `SELECT id, name, cwd, pm_id, COALESCE(description,''), created_at, updated_at
	                       FROM apps ORDER BY name ASC`)
}

func (r *AppRepository) ByNames(ctx context.Context, names []string) ([]*model.App, error) {
	if len(names) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(names))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	return r.query(ctx,
		`SELECT id, name, cwd, pm_id, COALESCE(description,''), created_at, updated_at
		   FROM apps WHERE name IN (`+placeholders+`) ORDER BY name ASC`, args...)
}

func (r *AppRepository) query(ctx context.Context, sqlStr string, args ...any) ([]*model.App, error) {
	rows, err := r.db.Query(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.App
	for rows.Next() {
		var a model.App
		if err := rows.Scan(&a.ID, &a.Name, &a.CWD, &a.PMID, &a.Description, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// SyncFromPM2 upserts the given apps into DB and removes ones no longer present.
// This mirrors the Node.js version's behaviour.
func (r *AppRepository) SyncFromPM2(ctx context.Context, apps []model.App) error {
	return r.db.WithTransaction(ctx, func(tx *sql.Tx) error {
		// Collect existing names
		rows, err := tx.QueryContext(ctx, `SELECT id, name FROM apps`)
		if err != nil {
			return err
		}
		existing := map[string]int64{}
		for rows.Next() {
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return err
			}
			existing[name] = id
		}
		rows.Close()

		incoming := map[string]model.App{}
		for _, a := range apps {
			incoming[a.Name] = a
		}

		// Insert or update
		for name, a := range incoming {
			if _, ok := existing[name]; ok {
				_, err := tx.ExecContext(ctx,
					`UPDATE apps SET cwd = ?, pm_id = ? WHERE name = ?`,
					a.CWD, a.PMID, name)
				if err != nil {
					return err
				}
			} else {
				_, err := tx.ExecContext(ctx,
					`INSERT INTO apps (name, cwd, pm_id, created_at, updated_at)
					       VALUES (?, ?, ?, NOW(), NOW())`,
					name, a.CWD, a.PMID)
				if err != nil {
					return err
				}
			}
		}

		// Delete missing
		for name, id := range existing {
			if _, ok := incoming[name]; !ok {
				if _, err := tx.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func scanApp(row *sql.Row) (*model.App, error) {
	var a model.App
	err := row.Scan(&a.ID, &a.Name, &a.CWD, &a.PMID, &a.Description, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
