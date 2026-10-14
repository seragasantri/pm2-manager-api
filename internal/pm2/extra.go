package pm2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// ProcessDetail adalah satu proses PM2 lengkap untuk halaman PM2.
type ProcessDetail struct {
	Name             string  `json:"name"`
	PmID             int     `json:"pmId"`
	Pid              int     `json:"pid"`
	Status           string  `json:"status"`
	CPU              float64 `json:"cpu"`
	Memory           float64 `json:"memory"`
	Uptime           int64   `json:"uptime"` // epoch ms sejak start
	Restarts         int     `json:"restarts"`
	UnstableRestarts int     `json:"unstableRestarts"`
	ExecMode         string  `json:"execMode"`
	Instances        any     `json:"instances,omitempty"`
	Namespace        string  `json:"namespace"`
	Version          string  `json:"version"`
	Cwd              string  `json:"cwd"`
	Script           string  `json:"script"`
	Args             any     `json:"args,omitempty"`
	Interpreter      string  `json:"interpreter"`
	NodeVersion      string  `json:"nodeVersion"`
	Watch            any     `json:"watch"`
	Autorestart      bool    `json:"autorestart"`
	OutLog           string  `json:"outLog"`
	ErrLog           string  `json:"errLog"`
	CreatedAt        int64   `json:"createdAt"`
	User             string  `json:"user"`
	IsModule         bool    `json:"isModule"`
}

type rawProcess struct {
	Name  string `json:"name"`
	PmID  int    `json:"pm_id"`
	Pid   int    `json:"pid"`
	Monit struct {
		CPU    float64 `json:"cpu"`
		Memory float64 `json:"memory"`
	} `json:"monit"`
	Env struct {
		Status           string `json:"status"`
		PmCwd            string `json:"pm_cwd"`
		PmUptime         int64  `json:"pm_uptime"`
		RestartTime      int    `json:"restart_time"`
		UnstableRestarts int    `json:"unstable_restarts"`
		ExecMode         string `json:"exec_mode"`
		Instances        any    `json:"instances"`
		Namespace        string `json:"namespace"`
		Version          string `json:"version"`
		PmExecPath       string `json:"pm_exec_path"`
		Args             any    `json:"args"`
		ExecInterpreter  string `json:"exec_interpreter"`
		NodeVersion      string `json:"node_version"`
		Watch            any    `json:"watch"`
		Autorestart      bool   `json:"autorestart"`
		PmOutLogPath     string `json:"pm_out_log_path"`
		PmErrLogPath     string `json:"pm_err_log_path"`
		CreatedAt        int64  `json:"created_at"`
		Username         string `json:"username"`
		AxmModule        bool   `json:"axm_module"`
	} `json:"pm2_env"`
}

// ListDetailed mengembalikan semua proses PM2 dengan detail lengkap.
func (c *Client) ListDetailed(ctx context.Context) ([]ProcessDetail, error) {
	out, err := c.exec(ctx, "jlist")
	if err != nil {
		return nil, err
	}
	// pm2 kadang mencetak peringatan sebelum JSON — ambil mulai '['.
	if i := strings.IndexByte(string(out), '['); i > 0 {
		out = out[i:]
	}
	var raw []rawProcess
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse pm2 jlist: %w", err)
	}
	res := make([]ProcessDetail, 0, len(raw))
	for _, p := range raw {
		e := p.Env
		res = append(res, ProcessDetail{
			Name: p.Name, PmID: p.PmID, Pid: p.Pid, Status: e.Status,
			CPU: p.Monit.CPU, Memory: p.Monit.Memory, Uptime: e.PmUptime,
			Restarts: e.RestartTime, UnstableRestarts: e.UnstableRestarts,
			ExecMode: strings.TrimSuffix(e.ExecMode, "_mode"), Instances: e.Instances,
			Namespace: e.Namespace, Version: e.Version, Cwd: e.PmCwd, Script: e.PmExecPath,
			Args: e.Args, Interpreter: e.ExecInterpreter, NodeVersion: e.NodeVersion,
			Watch: e.Watch, Autorestart: e.Autorestart, OutLog: e.PmOutLogPath,
			ErrLog: e.PmErrLogPath, CreatedAt: e.CreatedAt, User: e.Username,
			IsModule: e.AxmModule || strings.HasPrefix(strings.ToLower(p.Name), "pm2-"),
		})
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].PmID < res[j].PmID })
	return res, nil
}

// Find mengambil satu proses berdasar nama atau pm_id.
func (c *Client) Find(ctx context.Context, nameOrID string) (*ProcessDetail, error) {
	all, err := c.ListDetailed(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Name == nameOrID || fmt.Sprint(all[i].PmID) == nameOrID {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("proses pm2 '%s' tidak ditemukan", nameOrID)
}

// TailFile membaca maksimal n baris terakhir sebuah file log.
func TailFile(path string, n int) (string, error) {
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	const block = 64 * 1024
	size := st.Size()
	var buf []byte
	lines := 0
	for off := size; off > 0 && lines <= n; {
		read := int64(block)
		if off < read {
			read = off
		}
		off -= read
		chunk := make([]byte, read)
		if _, err := f.ReadAt(chunk, off); err != nil && err != io.EOF {
			return "", err
		}
		buf = append(chunk, buf...)
		lines = strings.Count(string(buf), "\n")
		if int64(len(buf)) > 8<<20 {
			break
		}
	}
	all := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return strings.Join(all, "\n"), nil
}

// Run menjalankan subcommand pm2 apa adanya (dipakai untuk aksi tambahan).
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	out, err := c.exec(ctx, args...)
	return string(out), err
}
