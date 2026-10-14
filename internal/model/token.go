package model

import "time"

// Token represents a row in the `tokens` table.
// AllowedApps/AllowedActions are stored as JSON in DB; the repository handles (de)serialisation.
type Token struct {
	ID              int64     `json:"id"`
	Code            string    `json:"code"`
	Label           string    `json:"label"`
	AllowedApps     []string  `json:"allowedApps"`
	AllowedActions  []string  `json:"allowedActions,omitempty"` // perms: logs,files,action,terminal,env,git,cron,db — empty = all
	CreatedBy       *int64    `json:"created_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}