package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/docker"
	"github.com/tragasolusi/pm2-manager-api/internal/model"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
	"github.com/tragasolusi/pm2-manager-api/internal/repository"
)

var (
	ErrAppNotFound    = errors.New("aplikasi tidak ditemukan")
	ErrActionInvalid  = errors.New("action harus start, stop, atau restart")
	ErrNoAccessToApp  = errors.New("anda tidak memiliki akses ke aplikasi ini")
	ErrAppUnavailable = errors.New("aplikasi tidak tersedia di PM2 atau Docker")
)

// AppView is the public-facing summary of an app.
type AppView struct {
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	CPU         float64 `json:"cpu"`
	Memory      float64 `json:"memory"`
	Uptime      any     `json:"uptime"`      // ms (docker) or epoch (pm2)
	CWD         string  `json:"cwd"`
	Type        string  `json:"type"`         // "docker" | "pm2"
	ContainerID string  `json:"containerId,omitempty"`
	Image       string  `json:"image,omitempty"`
	PmID        any     `json:"pm_id"`
}

type AppService struct {
	dockerCli *docker.Client
	pm2Cli    *pm2.Client
	apps      *repository.AppRepository
}

func NewAppService(d *docker.Client, p *pm2.Client, a *repository.AppRepository) *AppService {
	return &AppService{dockerCli: d, pm2Cli: p, apps: a}
}

// GetApps returns the full list (Docker + PM2), filtered by allowedApps for non-admin.
func (s *AppService) GetApps(ctx context.Context, claims *auth.Claims) ([]AppView, error) {
	views := make([]AppView, 0)
	seen := map[string]bool{}

	// 1. Docker containers
	if s.dockerCli != nil {
		cs, err := s.dockerCli.List(ctx)
		if err != nil {
			log.Printf("docker list error: %v", err)
		} else {
			for _, c := range cs {
				views = append(views, AppView{
					Name:        c.Name,
					Status:      mapDockerStatus(c.State),
					CWD:         "",
					Type:        "docker",
					ContainerID: c.ID,
					Image:       c.Image,
					Uptime:      c.Created * 1000,
					PmID:        shortID(c.ID),
				})
				seen[c.Name] = true
			}
		}
	}

	// 2. PM2 processes
	if s.pm2Cli != nil {
		procs, err := s.pm2Cli.List(ctx)
		if err != nil {
			log.Printf("pm2 list error: %v", err)
		} else {
			for _, p := range procs {
				if seen[p.Name] {
					continue
				}
				views = append(views, AppView{
					Name:   p.Name,
					Status: p.Pm2Env.Status,
					CPU:    p.Monit.CPU,
					Memory: p.Monit.Memory,
					Uptime: p.Pm2Env.PmUptime,
					CWD:    p.Pm2Env.PmCwd,
					Type:   "pm2",
					PmID:   p.PmID,
				})
			}
		}
	}

	// Sync to DB
	if err := s.syncToDB(ctx, views); err != nil {
		log.Printf("sync apps to db: %v", err)
	}

	// Filter for non-admin
	if claims != nil && claims.Role != "superadmin" && len(claims.AllowedApps) > 0 {
		allowed := make(map[string]bool, len(claims.AllowedApps))
		for _, a := range claims.AllowedApps {
			allowed[a] = true
		}
		filtered := views[:0]
		for _, v := range views {
			if allowed[v.Name] {
				filtered = append(filtered, v)
			}
		}
		views = filtered
	}

	return views, nil
}

func (s *AppService) syncToDB(ctx context.Context, views []AppView) error {
	models := make([]model.App, 0, len(views))
	for _, v := range views {
		pmID := ""
		switch id := v.PmID.(type) {
		case string:
			pmID = id
		case int:
			pmID = fmt.Sprintf("%d", id)
		case float64:
			pmID = fmt.Sprintf("%.0f", id)
		}
		models = append(models, model.App{
			Name: v.Name,
			CWD:  v.CWD,
			PMID: pmID,
		})
	}
	return s.apps.SyncFromPM2(ctx, models)
}

// DoAction runs start/stop/restart on the named app, preferring Docker.
func (s *AppService) DoAction(ctx context.Context, name, action string, claims *auth.Claims) error {
	if !isValidAction(action) {
		return ErrActionInvalid
	}
	if claims != nil && claims.Role != "superadmin" && !contains(claims.AllowedApps, name) {
		return ErrNoAccessToApp
	}

	// Try Docker first
	if s.dockerCli != nil {
		ctr, err := s.dockerCli.Get(ctx, name)
		if err != nil {
			log.Printf("docker get %s: %v", name, err)
		} else if ctr != nil {
			return s.dockerCli.Action(ctx, ctr.ID, action)
		}
	}

	// Fallback to PM2
	if s.pm2Cli != nil {
		if err := s.pm2Cli.Action(ctx, action, name); err == nil {
			return nil
		} else {
			return fmt.Errorf("gagal %s '%s': %w", action, name, err)
		}
	}
	return ErrAppUnavailable
}

func isValidAction(a string) bool {
	return a == "start" || a == "stop" || a == "restart"
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}

func mapDockerStatus(state string) string {
	switch state {
	case "running":
		return "online"
	case "exited", "paused", "dead":
		return "stopped"
	case "restarting":
		return "processing"
	default:
		return state
	}
}

func shortID(id string) string {
	if len(id) >= 12 {
		return id[:12]
	}
	return id
}
