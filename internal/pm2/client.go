package pm2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Process mirrors the JSON shape `pm2 jlist` emits.
type Process struct {
	Name  string `json:"name"`
	PmID  int    `json:"pm_id"`
	Monit struct {
		CPU    float64 `json:"cpu"`
		Memory float64 `json:"memory"`
	} `json:"monit"`
	Pm2Env struct {
		Status    string `json:"status"`
		PmCwd     string `json:"pm_cwd"`
		PmUptime  int64  `json:"pm_uptime"`
		PmID      int    `json:"pm_id"`
		AxmModule bool   `json:"axm_module"`
	} `json:"pm2_env"`
}

// Client runs `pm2` CLI with PM2_HOME set so it can talk to the host daemon.
type Client struct {
	bin  string
	home string
}

func NewClient(bin, home string) *Client {
	if bin == "" {
		bin = "pm2"
	}
	if home == "" {
		home = "/home/lpmobe/.pm2"
	}
	return &Client{bin: bin, home: home}
}

func (c *Client) exec(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	// IMPORTANT: setting cmd.Env replaces the full environment. Preserve host env
	// (PATH, etc.) and only override PM2_HOME so the CLI can reach the daemon.
	cmd.Env = append(os.Environ(),
		"PM2_HOME="+c.home,
		"PATH=/usr/local/bin:/usr/bin:/bin:"+os.Getenv("PATH"),
	)
	// Prefer absolute bin if it exists next to common install paths
	if !filepath.IsAbs(c.bin) {
		if p, err := exec.LookPath(c.bin); err == nil {
			cmd.Path = p
		}
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// IsModule reports whether this process is a PM2 module (e.g. pm2-logrotate).
func (p Process) IsModule() bool {
	if p.Pm2Env.AxmModule {
		return true
	}
	// Heuristic fallback
	n := strings.ToLower(p.Name)
	return strings.HasPrefix(n, "pm2-") || n == "pm2"
}

// List returns the parsed output of `pm2 jlist`.
func (c *Client) List(ctx context.Context) ([]Process, error) {
	out, err := c.exec(ctx, "jlist")
	if err != nil {
		return nil, err
	}
	var procs []Process
	if err := json.Unmarshal(out, &procs); err != nil {
		return nil, fmt.Errorf("parse pm2 jlist: %w", err)
	}
	return procs, nil
}

// Describe returns process info via pm2 jlist filtered by name.
// pm2 describe outputs a table (not JSON), so we use jlist + filter.
func (c *Client) Describe(ctx context.Context, name string) ([]Process, error) {
	out, err := c.exec(ctx, "jlist")
	if err != nil {
		return nil, err
	}
	var all []Process
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, fmt.Errorf("parse pm2 jlist: %w", err)
	}
	var filtered []Process
	for _, p := range all {
		if p.Name == name {
			filtered = append(filtered, p)
		}
	}
	return filtered, nil
}

// Action runs `pm2 <action> <name>` (start/stop/restart/reload/delete).
func (c *Client) Action(ctx context.Context, action, name string) error {
	_, err := c.exec(ctx, action, name)
	return err
}

// WithTimeout returns a context that cancels after d (for action calls).
func WithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
