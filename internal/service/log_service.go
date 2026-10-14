package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/docker"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
)

var (
	ErrLogAppNotFound = errors.New("aplikasi tidak ditemukan")
	ErrLogNoAccess    = errors.New("anda tidak memiliki akses ke aplikasi ini")
	ErrLogNoPerm      = errors.New("anda tidak memiliki izin akses logs untuk aplikasi ini")
)

// LogEntry is a single log line.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	App       string `json:"app"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// LogStats holds counts per level.
type LogStats struct {
	Total int            `json:"total"`
	ByLevel map[string]int `json:"byLevel"`
}

// LogService fetches logs from docker and pm2.
type LogService struct {
	dockerCli *docker.Client
	pm2Cli    *pm2.Client
}

func NewLogService(d *docker.Client, p *pm2.Client) *LogService {
	return &LogService{dockerCli: d, pm2Cli: p}
}

func (s *LogService) checkAccess(appName string, claims *auth.Claims) error {
	if claims == nil {
		return ErrLogNoAccess
	}
	if claims.Role == "superadmin" {
		return nil
	}
	if appName == "" {
		return nil // filtered later
	}
	for _, a := range claims.AllowedApps {
		if a == appName {
			if !auth.HasAction(claims, "logs") {
				return ErrLogNoPerm
			}
			return nil
		}
	}
	return ErrLogNoAccess
}

// GetLogs returns filtered/paginated logs. If app empty, combines all accessible apps.
func (s *LogService) GetLogs(ctx context.Context, app, level, search string, page, limit int, claims *auth.Claims) ([]LogEntry, int, error) {
	if app != "" {
		if err := s.checkAccess(app, claims); err != nil {
			return nil, 0, err
		}
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	level = strings.ToLower(strings.TrimSpace(level))
	search = strings.TrimSpace(search)

	var targets []string
	if app != "" {
		targets = []string{app}
	} else {
		targets = s.listAccessibleApps(ctx, claims)
	}

	var all []LogEntry
	for _, name := range targets {
		entries := s.fetchAppLogs(ctx, name)
		all = append(all, entries...)
	}

	// If no logs at all, return mock info.
	if len(all) == 0 {
		mockApp := app
		if mockApp == "" {
			mockApp = "system"
		}
		all = []LogEntry{{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			App:       mockApp,
			Level:     "info",
			Message:   "belum ada logs untuk aplikasi ini",
		}}
	}

	// Filter by level/search.
	filtered := make([]LogEntry, 0, len(all))
	for _, e := range all {
		if level != "" && level != "all" && !strings.EqualFold(e.Level, level) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(search)) {
			continue
		}
		filtered = append(filtered, e)
	}

	// Sort newest first by timestamp (parse, fallback to insertion order).
	sort.SliceStable(filtered, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, filtered[i].Timestamp)
		tj, _ := time.Parse(time.RFC3339, filtered[j].Timestamp)
		if ti.IsZero() || tj.IsZero() {
			return i < j
		}
		return ti.After(tj)
	})

	total := len(filtered)
	start := (page - 1) * limit
	if start >= total {
		return []LogEntry{}, total, nil
	}
	end := start + limit
	if end > total {
		end = total
	}
	return filtered[start:end], total, nil
}

// GetLogStats returns counts per level for the given app (or all).
func (s *LogService) GetLogStats(ctx context.Context, app string, claims *auth.Claims) (*LogStats, error) {
	if app != "" {
		if err := s.checkAccess(app, claims); err != nil {
			return nil, err
		}
	}
	var targets []string
	if app != "" {
		targets = []string{app}
	} else {
		targets = s.listAccessibleApps(ctx, claims)
	}
	var all []LogEntry
	for _, name := range targets {
		all = append(all, s.fetchAppLogs(ctx, name)...)
	}
	if len(all) == 0 {
		return &LogStats{Total: 0, ByLevel: map[string]int{"info": 0, "warn": 0, "error": 0}}, nil
	}
	byLevel := map[string]int{"info": 0, "warn": 0, "error": 0, "debug": 0}
	for _, e := range all {
		lvl := strings.ToLower(e.Level)
		if _, ok := byLevel[lvl]; !ok {
			byLevel[lvl] = 0
		}
		byLevel[lvl]++
	}
	return &LogStats{Total: len(all), ByLevel: byLevel}, nil
}

func (s *LogService) listAccessibleApps(ctx context.Context, claims *auth.Claims) []string {
	seen := map[string]bool{}
	var out []string
	if s.dockerCli != nil {
		if cs, err := s.dockerCli.List(ctx); err == nil {
			for _, c := range cs {
				if !seen[c.Name] {
					seen[c.Name] = true
					out = append(out, c.Name)
				}
			}
		}
	}
	if s.pm2Cli != nil {
		if procs, err := s.pm2Cli.List(ctx); err == nil {
			for _, p := range procs {
				if p.IsModule() {
					continue
				}
				if !seen[p.Name] {
					seen[p.Name] = true
					out = append(out, p.Name)
				}
			}
		}
	}
	if claims != nil && claims.Role != "superadmin" && len(claims.AllowedApps) > 0 {
		allowed := map[string]bool{}
		for _, a := range claims.AllowedApps {
			allowed[a] = true
		}
		filtered := out[:0]
		for _, n := range out {
			if allowed[n] {
				filtered = append(filtered, n)
			}
		}
		out = filtered
		// Also include allowed apps even if not currently running (so logs can be attempted).
		for _, a := range claims.AllowedApps {
			if !seen[a] {
				out = append(out, a)
			}
		}
	}
	return out
}

func (s *LogService) fetchAppLogs(ctx context.Context, app string) []LogEntry {
	// Try docker first.
	if s.dockerCli != nil {
		if ctr, err := s.dockerCli.Get(ctx, app); err == nil && ctr != nil {
			if entries := fetchDockerLogs(ctx, ctr.ID, app); len(entries) > 0 {
				return entries
			}
		}
	}
	// PM2 fallback (or if docker had no logs, also try pm2).
	if s.pm2Cli != nil {
		if procs, err := s.pm2Cli.Describe(ctx, app); err == nil && len(procs) > 0 {
			if entries := fetchPM2Logs(ctx, app); len(entries) > 0 {
				return entries
			}
		} else if app != "" {
			// Even if not in jlist, try reading pm2 log files directly.
			if entries := fetchPM2Logs(ctx, app); len(entries) > 0 {
				return entries
			}
		}
	}
	// Last resort: try pm2 log files anyway.
	if entries := fetchPM2Logs(ctx, app); len(entries) > 0 {
		return entries
	}
	return nil
}

func fetchDockerLogs(ctx context.Context, containerID, app string) []LogEntry {
	cli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil
	}
	defer cli.Close()
	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       "300",
	}
	rc, err := cli.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		return nil
	}
	defer rc.Close()
	// Docker multiplexes when not TTY. Read raw and strip header if needed.
	raw, err := io.ReadAll(rc)
	if err != nil && len(raw) == 0 {
		return nil
	}
	// Try stripping docker header; if no header, raw is plain text.
	text := stripDockerHeaderIfPresent(raw)
	// Also try decoding as plain lines if header stripping produced empty.
	if strings.TrimSpace(text) == "" {
		text = string(raw)
	}
	lines := strings.Split(text, "\n")
	var out []LogEntry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, parseDockerLine(line, app))
	}
	return out
}

func parseDockerLine(line, app string) LogEntry {
	// Format with timestamps: "2024-01-02T15:04:05.000000000Z message"
	var ts, msg string
	if len(line) > 30 && line[10] == 'T' {
		// Find first space after timestamp.
		if idx := strings.Index(line, " "); idx > 0 {
			candidate := line[:idx]
			if _, err := time.Parse(time.RFC3339Nano, candidate); err == nil {
				ts = candidate
				msg = strings.TrimSpace(line[idx+1:])
			} else if _, err := time.Parse(time.RFC3339, candidate); err == nil {
				ts = candidate
				msg = strings.TrimSpace(line[idx+1:])
			}
		}
	}
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
		msg = line
	}
	return LogEntry{
		Timestamp: ts,
		App:       app,
		Level:     detectLevel(msg),
		Message:   msg,
	}
}

func stripDockerHeaderIfPresent(b []byte) string {
	// Reuse logic from docker/client.go: 8-byte header per frame.
	if len(b) < 8 {
		return string(b)
	}
	// Heuristic: check if bytes look like multiplexed header.
	// Header byte 0 is stream type (1 or 2), bytes 1-3 are 0, bytes 4-7 are size.
	hasHeader := false
	for i := 0; i+8 < len(b); {
		if b[i] != 1 && b[i] != 2 {
			break
		}
		if b[i+1] != 0 || b[i+2] != 0 || b[i+3] != 0 {
			break
		}
		size := int(uint32(b[i+4])<<24 | uint32(b[i+5])<<16 | uint32(b[i+6])<<8 | uint32(b[i+7]))
		if size <= 0 || i+8+size > len(b)+16 {
			break
		}
		hasHeader = true
		break
	}
	if !hasHeader {
		return string(b)
	}
	var out bytes.Buffer
	for len(b) > 8 {
		size := int(uint32(b[4])<<24 | uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7]))
		if size < 0 || 8+size > len(b) {
			break
		}
		out.Write(b[8 : 8+size])
		b = b[8+size:]
	}
	return out.String()
}

func fetchPM2Logs(ctx context.Context, app string) []LogEntry {
	// Try reading log files from PM2_HOME.
	homes := []string{}
	if v := os.Getenv("PM2_HOME"); v != "" {
		homes = append(homes, v)
	}
	homes = append(homes, "/home/lpmobe/.pm2", "/root/.pm2", filepath.Join(os.Getenv("HOME"), ".pm2"))
	// Also try to discover via pm2 jlist pm_out_log_path if available.
	if raw, err := runPM2JList(ctx); err == nil {
		var arr []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, m := range arr {
				var name string
				if err := json.Unmarshal(m["name"], &name); err == nil && name == app {
					var env map[string]any
					if pm2envRaw, ok := m["pm2_env"]; ok {
						_ = json.Unmarshal(pm2envRaw, &env)
						for _, k := range []string{"pm_out_log_path", "pm_err_log_path", "pm_log_path"} {
							if p, ok := env[k].(string); ok && p != "" {
								if entries := tailLogFile(p, app, 200); len(entries) > 0 {
									return entries
								}
							}
						}
					}
				}
			}
		}
	}

	for _, home := range homes {
		if home == "" {
			continue
		}
		candidates := []string{
			filepath.Join(home, "logs", app+"-out.log"),
			filepath.Join(home, "logs", app+"-error.log"),
			filepath.Join(home, "logs", app+".log"),
		}
		var combined []LogEntry
		for _, p := range candidates {
			entries := tailLogFile(p, app, 150)
			combined = append(combined, entries...)
		}
		if len(combined) > 0 {
			// Keep most recent 300, sorted newest first later by caller.
			if len(combined) > 300 {
				combined = combined[len(combined)-300:]
			}
			return combined
		}
	}
	// Final fallback: try pm2 logs command via exec (may hang, use timeout).
	if entries := fetchPM2LogsViaExec(ctx, app); len(entries) > 0 {
		return entries
	}
	return nil
}

func runPM2JList(ctx context.Context) ([]byte, error) {
	bin := os.Getenv("PM2_BIN")
	if bin == "" {
		bin = "pm2"
	}
	home := os.Getenv("PM2_HOME")
	if home == "" {
		home = "/home/lpmobe/.pm2"
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "jlist")
	cmd.Env = append(os.Environ(), "PM2_HOME="+home, "PATH=/usr/local/bin:/usr/bin:/bin:"+os.Getenv("PATH"))
	return cmd.CombinedOutput()
}

func fetchPM2LogsViaExec(ctx context.Context, app string) []LogEntry {
	bin := os.Getenv("PM2_BIN")
	if bin == "" {
		bin = "pm2"
	}
	home := os.Getenv("PM2_HOME")
	if home == "" {
		home = "/home/lpmobe/.pm2"
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	// Use --json if available; fallback to plain.
	cmd := exec.CommandContext(cctx, bin, "logs", app, "--lines", "200", "--nostream", "--json")
	cmd.Env = append(os.Environ(), "PM2_HOME="+home, "PATH=/usr/local/bin:/usr/bin:/bin:"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) == 0 {
		return nil
	}
	// Try parse JSON lines.
	var entries []LogEntry
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err == nil {
			msg, _ := obj["data"].(string)
			if msg == "" {
				msg, _ = obj["message"].(string)
			}
			if msg == "" {
				msg = line
			}
			ts, _ := obj["timestamp"].(string)
			if ts == "" {
				ts = time.Now().UTC().Format(time.RFC3339)
			}
			appName := app
			if a, ok := obj["app"].(string); ok && a != "" {
				appName = a
			}
			entries = append(entries, LogEntry{
				Timestamp: ts,
				App:       appName,
				Level:     detectLevel(msg),
				Message:   msg,
			})
		} else {
			entries = append(entries, LogEntry{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				App:       app,
				Level:     detectLevel(line),
				Message:   line,
			})
		}
	}
	return entries
}

func tailLogFile(path, app string, n int) []LogEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	lines, err := tailLines(f, n)
	if err != nil {
		return nil
	}
	var out []LogEntry
	for _, line := range lines {
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Try to extract timestamp prefix: "2024-... " or "[timestamp] ".
		ts := time.Now().UTC().Format(time.RFC3339)
		msg := line
		// pm2 logs often prefix with timestamp or level.
		if len(line) > 20 {
			// Look for ISO timestamp at start.
			candidate := strings.Fields(line)[0]
			if _, err := time.Parse(time.RFC3339, candidate); err == nil {
				ts = candidate
				msg = strings.TrimSpace(strings.TrimPrefix(line, candidate))
			} else if _, err := time.Parse("2006-01-02 15:04:05", candidate+" "+strings.Fields(line)[1]); err == nil && len(strings.Fields(line)) > 1 {
				// ignore
			}
		}
		out = append(out, LogEntry{
			Timestamp: ts,
			App:       app,
			Level:     detectLevel(msg),
			Message:   msg,
		})
	}
	return out
}

func tailLines(r io.ReadSeeker, n int) ([]string, error) {
	// Simple: read all and keep last n (log files small for pm2).
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	all := strings.Split(string(b), "\n")
	if len(all) <= n {
		return all, nil
	}
	return all[len(all)-n:], nil
}

func detectLevel(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "err]") || strings.Contains(lower, "fail"):
		return "error"
	case strings.Contains(lower, "warn"):
		return "warn"
	case strings.Contains(lower, "debug"):
		return "debug"
	default:
		return "info"
	}
}

