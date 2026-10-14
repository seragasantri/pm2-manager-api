package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/docker"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
)

var (
	ErrEnvAppNotFound = errors.New("aplikasi tidak ditemukan")
	ErrEnvNoAccess    = errors.New("anda tidak memiliki akses ke aplikasi ini")
	ErrEnvInvalidKey  = errors.New("key tidak valid")
)

var (
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	numRe    = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
)

// EnvVar is a single .env entry.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type EnvService struct {
	dockerCli *docker.Client
	pm2Cli    *pm2.Client
}

func NewEnvService(d *docker.Client, p *pm2.Client) *EnvService {
	return &EnvService{dockerCli: d, pm2Cli: p}
}

func (s *EnvService) checkAccess(app string, c *auth.Claims) error {
	if c == nil {
		return ErrEnvNoAccess
	}
	if c.Role == "superadmin" {
		return nil
	}
	for _, a := range c.AllowedApps {
		if a == app {
			return nil
		}
	}
	return ErrEnvNoAccess
}

type envResolved struct {
	kind, cwd, containerID string
}

func (s *EnvService) resolve(ctx context.Context, app string) (*envResolved, error) {
	if s.dockerCli != nil {
		if ctr, _ := s.dockerCli.Get(ctx, app); ctr != nil {
			cwd, err := s.dockerCli.CWD(ctx, ctr.ID)
			if err != nil {
				cwd = "/app"
			}
			return &envResolved{"docker", cwd, ctr.ID}, nil
		}
	}
	if s.pm2Cli != nil {
		if procs, _ := s.pm2Cli.Describe(ctx, app); len(procs) > 0 {
			cwd := procs[0].Pm2Env.PmCwd
			if cwd == "" {
				cwd = "/"
			}
			return &envResolved{"pm2", cwd, ""}, nil
		}
	}
	return nil, ErrEnvAppNotFound
}

func (s *EnvService) GetEnvVars(ctx context.Context, app string, claims *auth.Claims) ([]EnvVar, error) {
	if err := s.checkAccess(app, claims); err != nil {
		return nil, err
	}
	r, err := s.resolve(ctx, app)
	if err != nil {
		return nil, err
	}
	var raw string
	if r.kind == "docker" {
		for _, p := range []string{filepath.Join(r.cwd, ".env"), "/app/.env", ".env"} {
			out, _ := s.dockerCli.Exec(ctx, r.containerID, fmt.Sprintf(`cat %q 2>/dev/null`, p))
			if strings.TrimSpace(out) != "" {
				raw = out
				break
			}
		}
	} else {
		b, err := os.ReadFile(filepath.Join(r.cwd, ".env"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return []EnvVar{}, nil
			}
			return nil, fmt.Errorf("read .env: %w", err)
		}
		raw = string(b)
	}
	if strings.TrimSpace(raw) == "" {
		return []EnvVar{}, nil
	}
	return parseEnv(raw), nil
}

func (s *EnvService) UpdateEnvVars(ctx context.Context, app string, vars []EnvVar, claims *auth.Claims) error {
	if err := s.checkAccess(app, claims); err != nil {
		return err
	}
	for _, v := range vars {
		if !envKeyRe.MatchString(v.Key) {
			return fmt.Errorf("%w: %q", ErrEnvInvalidKey, v.Key)
		}
	}
	r, err := s.resolve(ctx, app)
	if err != nil {
		return err
	}
	content := buildEnvContent(vars)
	if r.kind == "docker" {
		envPath := filepath.Join(r.cwd, ".env")
		if r.cwd == "/app" || strings.HasPrefix(envPath, "/app") {
			envPath = "/app/.env"
		}
		_, _ = s.dockerCli.Exec(ctx, r.containerID, fmt.Sprintf(`if [ -f %q ]; then cp %q %q.bak; fi`, envPath, envPath, envPath))
		tmp := envPath + ".tmp"
		delim := "ENV_EOF_8f3a1b"
		for strings.Contains(content, delim) {
			delim += "x"
		}
		cmd := fmt.Sprintf("cat > %q <<'%s'\n%s\n%s\nmv %q %q", tmp, delim, content, delim, tmp, envPath)
		if _, err := s.dockerCli.Exec(ctx, r.containerID, cmd); err != nil {
			return fmt.Errorf("write .env: %w", err)
		}
		_ = s.dockerCli.Action(ctx, r.containerID, "restart")
		return nil
	}
	envPath := filepath.Join(r.cwd, ".env")
	_ = os.MkdirAll(filepath.Dir(envPath), 0o755)
	if _, err := os.Stat(envPath); err == nil {
		if b, err := os.ReadFile(envPath); err == nil {
			_ = os.WriteFile(envPath+".bak", b, 0o644)
		}
	}
	tmp := envPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, envPath)
}

func parseEnv(raw string) []EnvVar {
	var out []EnvVar
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		i := strings.Index(line, "=")
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		if !envKeyRe.MatchString(k) {
			continue
		}
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		out = append(out, EnvVar{k, v, inferType(v)})
	}
	return out
}

func buildEnvContent(vars []EnvVar) string {
	var sb strings.Builder
	for _, v := range vars {
		val := v.Value
		if strings.ContainsAny(val, " \t#\"'") || strings.Contains(val, "\n") {
			val = `"` + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(val, `\`, `\\`), `"`, `\"`), "\n", `\n`) + `"`
		}
		sb.WriteString(v.Key + "=" + val + "\n")
	}
	return sb.String()
}

func inferType(v string) string {
	if v == "" {
		return "string"
	}
	switch strings.ToLower(v) {
	case "true", "false":
		return "boolean"
	}
	if numRe.MatchString(strings.TrimSpace(v)) {
		return "number"
	}
	return "string"
}
