package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/tragasolusi/pm2-manager-api/internal/database"
	"github.com/tragasolusi/pm2-manager-api/internal/model"
)

// ErrNotFound is returned when a row lookup returns nothing.
var ErrNotFound = errors.New("not found")

type UserRepository struct {
	db *database.DB
}

func NewUserRepository(db *database.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, username, password, role, created_at, updated_at
		   FROM users WHERE username = ?`, username)
	return scanUser(row)
}

func (r *UserRepository) FindByID(ctx context.Context, id int64) (*model.User, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, username, password, role, created_at, updated_at
		   FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Username, &u.Password, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}
