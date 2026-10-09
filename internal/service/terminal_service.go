package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/docker"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
)

var ErrTerminalAppNotFound = errors.New("aplikasi tidak ditemukan untuk terminal")

var ErrTerminalGeneralForbidden = errors.New("terminal umum hanya untuk superadmin")

// TerminalService resolves apps to terminal sessions and runs commands.
type TerminalService struct {
	dockerCli *docker.Client
	pm2Cli    *pm2.Client
}

// Docker exposes the underlying docker client for sibling services that need
// direct exec (e.g. GitService running `git pull` inside a container).
func (s *TerminalService) Docker() *docker.Client { return s.dockerCli }

func NewTerminalService(d *docker.Client, p *pm2.Client) *TerminalService {
	return &TerminalService{dockerCli: d, pm2Cli: p}
}

// Open resolves the app, applies access checks, and returns a Session.
// Semua app (docker/PM2) → host shell di folder project.
func (s *TerminalService) Open(ctx context.Context, appName string, claims *auth.Claims) (*Session, error) {
	if claims != nil && claims.Role != "superadmin" {
		ok := false
		for _, a := range claims.AllowedApps {
			if a == appName {
				ok = true
				break
			}
		}
		if !ok {
			return nil, ErrTerminalNotAllowed
		}
	}

	// 1. Docker → cari host project root (compose dir / bind mount)
	if s.dockerCli != nil {
		hostRoot, err := s.resolveDockerHostRoot(ctx, appName)
		if err == nil && hostRoot != "" {
			return &Session{
				App:  appName,
				Root: hostRoot,
				Kind: "host",
			}, nil
		}
	}

	// 2. PM2 → host shell di pm_cwd
	if s.pm2Cli != nil {
		procs, err := s.pm2Cli.Describe(ctx, appName)
		if err == nil && len(procs) > 0 {
			cwd := procs[0].Pm2Env.PmCwd
			if cwd == "" {
				cwd = "/"
			}
			resolved, ferr := filepath.EvalSymlinks(cwd)
			if ferr != nil {
				resolved = cwd
			}
			return &Session{
				App:  appName,
				Root: resolved,
				Kind: "host",
			}, nil
		}
	}

	return nil, ErrTerminalAppNotFound
}

// resolveDockerHostRoot mencari host directory dari container.
// Prioritas: compose working_dir → bind mount → fallback CWD di host.
func (s *TerminalService) resolveDockerHostRoot(ctx context.Context, appName string) (string, error) {
	ctr, err := s.dockerCli.Get(ctx, appName)
	if err != nil || ctr == nil {
		return "", fmt.Errorf("container tidak ditemukan: %w", err)
	}

	// 1. Compose working_dir
	ci, err := s.dockerCli.ComposeInfo(ctx, ctr.ID)
	if err == nil && ci != nil && ci.WorkingDir != "" && dirExists(ci.WorkingDir) {
		return ci.WorkingDir, nil
	}

	// 2. Bind mount → cari yang ada .git atau docker-compose.yml
	binds, err := s.dockerCli.HostBinds(ctx, ctr.ID)
	if err == nil {
		for _, b := range binds {
			if isGitRepo(b) || hasComposeFile(b) {
				return b, nil
			}
			parent := filepath.Dir(b)
			if isGitRepo(parent) || hasComposeFile(parent) {
				return parent, nil
			}
		}
		// 3. Bind mount pertama yang exist
		for _, b := range binds {
			if dirExists(b) {
				return b, nil
			}
		}
	}

	return "", fmt.Errorf("tidak bisa resolve host root untuk %s", appName)
}

// OpenGeneral membuka sesi terminal UMUM: akses penuh ke server (root /),
// tanpa filter keamanan ketat — setara SSH. Hanya untuk superadmin.
// appName boleh "*" atau nama app tertentu (dipakai sebagai label + CWD awal).
func (s *TerminalService) OpenGeneral(appName string, claims *auth.Claims) (*Session, error) {
	if claims == nil || claims.Role != "superadmin" {
		return nil, ErrTerminalGeneralForbidden
	}

	root := "/"
	label := appName
	if label == "" || label == "*" {
		label = "*"
	} else {
		// Kalau nama app dikenali, jadikan foldernya sebagai CWD awal
		// (user tetap bebas cd ke mana saja setelahnya).
		ctx := context.TODO()
		if s.dockerCli != nil {
			if hostRoot, err := s.resolveDockerHostRoot(ctx, label); err == nil && hostRoot != "" {
				root = hostRoot
			}
		}
		if root == "/" && s.pm2Cli != nil {
			if procs, err := s.pm2Cli.Describe(ctx, label); err == nil && len(procs) > 0 {
				if cwd := procs[0].Pm2Env.PmCwd; cwd != "" {
					if resolved, ferr := filepath.EvalSymlinks(cwd); ferr == nil {
						root = resolved
					} else {
						root = cwd
					}
				}
			}
		}
	}

	return &Session{
		App:     label,
		Root:    root,
		Kind:    "host",
		General: true,
	}, nil
}

// Exec runs a command in the given session, streaming output to w.
// Semua session → host shell.
func (s *TerminalService) Exec(ctx context.Context, sess *Session, raw string, w io.Writer) (int, error) {
	return sess.ExecHost(ctx, raw, w)
}
