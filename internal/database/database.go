package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/tragasolusi/pm2-manager-api/internal/config"
)

// DB wraps the *sql.DB pool.
type DB struct {
	*sql.DB
}

// Connect opens a MySQL connection pool.
func Connect(cfg *config.AppConfig) (*DB, error) {
	db, err := sql.Open("mysql", cfg.DB.DSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return &DB{db}, nil
}

// Query runs a parameterised query and returns the rows.
func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, query, args...)
}

// QueryRow runs a parameterised query that returns at most one row.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, query, args...)
}

// Exec runs a parameterised non-row-returning statement.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, query, args...)
}

// WithTransaction runs fn inside a transaction; commits on success, rolls back on error.
func (d *DB) WithTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
