package model

import "time"

// App represents a row in the `apps` table.
type App struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	CWD         string    `json:"cwd"`
	PMID        string    `json:"pm_id"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
