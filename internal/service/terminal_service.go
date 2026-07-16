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

	// Prefer Docker; for container apps the root is the container's CWD label.
	if s.dockerCli != nil {
		ctr, err := s.dockerCli.Get(ctx, appName)
		if err == nil && ctr != nil {
			cwd, err := s.dockerCli.CWD(ctx, ctr.ID)
			if err != nil {
				return nil, fmt.Errorf("inspect container: %w", err)
			}
			return &Session{
				App:    appName,
				Root:   cwd,
				Kind:   "docker",
				ContID: ctr.ID,
			}, nil
		}
	}

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
				Kind: "pm2",
			}, nil
		}
	}

	return nil, ErrTerminalAppNotFound
}

// Exec runs a command in the given session, streaming output to w.
// Returns the exit code; for Docker this is the container exit code.
func (s *TerminalService) Exec(ctx context.Context, sess *Session, raw string, w io.Writer) (int, error) {
	if sess.Kind == "docker" {
		if err := sess.validateCmd(raw); err != nil {
			return -1, err
		}
		wrapped := fmt.Sprintf("cd %q && %s", sess.Root, raw)
		return s.dockerCli.ExecStream(ctx, sess.ContID, wrapped, w)
	}
	return sess.ExecHost(ctx, raw, w)
}
