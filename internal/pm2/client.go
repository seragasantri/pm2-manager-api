package pm2

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Process mirrors the JSON shape `pm2 jlist` emits.
type Process struct {
	Name       string `json:"name"`
	PmID       int    `json:"pm_id"`
	Monit      struct {
		CPU    float64 `json:"cpu"`
		Memory float64 `json:"memory"`
	} `json:"monit"`
	Pm2Env struct {
		Status   string `json:"status"`
		PmCwd    string `json:"pm_cwd"`
		PmUptime int64  `json:"pm_uptime"`
		PmID     int    `json:"pm_id"`
	} `json:"pm2_env"`
}

// Client runs `pm2` CLI with PM2_HOME set so it can talk to the host daemon.
type Client struct {
	bin  string
	home string
}

func NewClient(bin, home string) *Client {
	return &Client{bin: bin, home: home}
}

func (c *Client) exec(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Env = append(cmd.Env, "PM2_HOME="+c.home)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
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

// Describe returns the parsed output of `pm2 describe <name>`.
func (c *Client) Describe(ctx context.Context, name string) ([]Process, error) {
	out, err := c.exec(ctx, "describe", name)
	if err != nil {
		return nil, err
	}
	var procs []Process
	if err := json.Unmarshal(out, &procs); err != nil {
		return nil, fmt.Errorf("parse pm2 describe: %w", err)
	}
	return procs, nil
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
