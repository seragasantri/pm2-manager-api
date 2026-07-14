package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/docker"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
)

var (
	ErrPathInvalid      = errors.New("path tidak valid")
	ErrFileAccessDenied = errors.New("akses direktori ditolak")
	ErrFileAppNotFound  = errors.New("aplikasi tidak ditemukan")
)

// FileEntry is a single item in a directory listing.
type FileEntry struct {
	Name        string `json:"name"`
	IsDirectory bool   `json:"isDirectory"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
}

// FileService manages file ops across Docker containers and PM2 host processes.
type FileService struct {
	dockerCli *docker.Client
	pm2Cli    *pm2.Client
}

func NewFileService(d *docker.Client, p *pm2.Client) *FileService {
	return &FileService{dockerCli: d, pm2Cli: p}
}

type resolvedApp struct {
	kind        string // "docker" | "pm2"
	cwd         string
	containerID string
}

// resolveApp figures out the kind and root cwd of an app.
func (s *FileService) resolveApp(ctx context.Context, name string) (*resolvedApp, error) {
	// Docker first
	if s.dockerCli != nil {
		ctr, err := s.dockerCli.Get(ctx, name)
		if err == nil && ctr != nil {
			cwd, err := s.dockerCli.CWD(ctx, ctr.ID)
			if err != nil {
				return nil, fmt.Errorf("inspect container: %w", err)
			}
			return &resolvedApp{kind: "docker", cwd: cwd, containerID: ctr.ID}, nil
		}
	}

	// PM2 fallback
	if s.pm2Cli != nil {
		procs, err := s.pm2Cli.Describe(ctx, name)
		if err == nil && len(procs) > 0 {
			cwd := procs[0].Pm2Env.PmCwd
			if cwd == "" {
				cwd = "/"
			}
			return &resolvedApp{kind: "pm2", cwd: cwd}, nil
		}
	}
	return nil, ErrFileAppNotFound
}

// safeJoin joins base + rel and ensures the result is still under base.
// Returns the cleaned absolute path.
func safeJoin(base, rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	joined := filepath.Join(base, rel)
	cleanedBase := filepath.Clean(base)
	cleaned := filepath.Clean(joined)
	if cleaned != cleanedBase && !strings.HasPrefix(cleaned+string(os.PathSeparator), cleanedBase+string(os.PathSeparator)) {
		return "", ErrFileAccessDenied
	}
	return cleaned, nil
}

// List returns the contents of dir within the app's root.
func (s *FileService) List(ctx context.Context, name, dir string, claims *auth.Claims) (map[string]any, error) {
	app, err := s.resolveApp(ctx, name)
	if err != nil {
		return nil, err
	}
	target, err := safeJoin(app.cwd, dir)
	if err != nil {
		return nil, err
	}

	var files []FileEntry
	if app.kind == "docker" {
		files, err = s.listInContainer(ctx, app.containerID, target, dir)
	} else {
		files, err = s.listOnHost(ctx, target, dir)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"cwd":   app.cwd,
		"dir":   dir,
		"files": files,
	}, nil
}

func (s *FileService) listOnHost(_ context.Context, absPath, relBase string) ([]FileEntry, error) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		var size int64
		if !e.IsDir() {
			info, err := e.Info()
			if err == nil {
				size = info.Size()
			}
		}
		out = append(out, FileEntry{
			Name:        e.Name(),
			IsDirectory: e.IsDir(),
			Path:        filepath.ToSlash(filepath.Join(relBase, e.Name())),
			Size:        size,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDirectory != out[j].IsDirectory {
			return out[i].IsDirectory
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *FileService) listInContainer(ctx context.Context, id, absPath, relBase string) ([]FileEntry, error) {
	// `ls -la` with -p to mark dirs
	out, err := s.dockerCli.Exec(ctx, id, fmt.Sprintf(`ls -la %q 2>&1`, absPath))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	entries := make([]FileEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " ")
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}
		// format: perms links owner group size month day time name
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		perms := fields[0]
		sizeStr := fields[4]
		name := strings.Join(fields[8:], " ")
		if name == "." || name == ".." {
			continue
		}
		var size int64
		_, _ = fmt.Sscanf(sizeStr, "%d", &size)
		entries = append(entries, FileEntry{
			Name:        name,
			IsDirectory: strings.HasPrefix(perms, "d"),
			Path:        filepath.ToSlash(filepath.Join(relBase, name)),
			Size:        size,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDirectory != entries[j].IsDirectory {
			return entries[i].IsDirectory
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Read returns the file content as a string.
func (s *FileService) Read(ctx context.Context, name, relPath string) (string, error) {
	app, err := s.resolveApp(ctx, name)
	if err != nil {
		return "", err
	}
	target, err := safeJoin(app.cwd, relPath)
	if err != nil {
		return "", err
	}
	if app.kind == "docker" {
		return s.dockerCli.Exec(ctx, app.containerID, fmt.Sprintf(`cat %q`, target))
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Write persists content to relPath (overwrites).
func (s *FileService) Write(ctx context.Context, name, relPath, content string) error {
	app, err := s.resolveApp(ctx, name)
	if err != nil {
		return err
	}
	target, err := safeJoin(app.cwd, relPath)
	if err != nil {
		return err
	}
	if app.kind == "docker" {
		// Use heredoc with a delimiter that won't appear in the content.
		delim := "PM2MGR_EOF_" + randomToken()
		cmd := fmt.Sprintf(
			`mkdir -p $(dirname %q) && cat > %q <<'%s'
%s
%s`,
			target, target, delim, content, delim,
		)
		_, err := s.dockerCli.Exec(ctx, app.containerID, cmd)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(content), 0o644)
}

// Delete removes a file or directory.
func (s *FileService) Delete(ctx context.Context, name, relPath string) error {
	app, err := s.resolveApp(ctx, name)
	if err != nil {
		return err
	}
	target, err := safeJoin(app.cwd, relPath)
	if err != nil {
		return err
	}
	if app.kind == "docker" {
		_, err := s.dockerCli.Exec(ctx, app.containerID, fmt.Sprintf(`rm -rf %q`, target))
		return err
	}
	return os.RemoveAll(target)
}

// CreateDir creates a directory (and parents) at relPath.
func (s *FileService) CreateDir(ctx context.Context, name, relPath string) error {
	app, err := s.resolveApp(ctx, name)
	if err != nil {
		return err
	}
	target, err := safeJoin(app.cwd, relPath)
	if err != nil {
		return err
	}
	if app.kind == "docker" {
		_, err := s.dockerCli.Exec(ctx, app.containerID, fmt.Sprintf(`mkdir -p %q`, target))
		return err
	}
	return os.MkdirAll(target, 0o755)
}

func randomToken() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
