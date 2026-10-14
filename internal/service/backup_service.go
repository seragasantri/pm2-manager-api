package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	driveLink  = "https://drive.google.com/drive/folders/13bE-uaqdYUfVWZvnZCiWqAq4sz3jMWUQ"
	dailyBase  = "/home/lpmobe/backups/daily"
	hermesBase = "/home/lpmobe/backups/hermes"
	logDir     = "/home/lpmobe/logs/backup"
)

var (
	ErrBackupLogNotFound = errors.New("log backup tidak ditemukan")
	dateRe               = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// BackupProject holds status for one backup target.
type BackupProject struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	LastBackup string   `json:"lastBackup"`
	Status     string   `json:"status"` // OK | FAILED | MISSING
	Size       int64    `json:"size"`
	SizeHuman  string   `json:"sizeHuman"`
	DriveLink  string   `json:"driveLink"`
	LogTail    []string `json:"logTail"`
}

type projDef struct {
	Name string
	Path string
}

var backupProjects = []projDef{
	{"simobe", "/var/www/simobe-lpm"},
	{"pkdp", "/var/www/lpm/pkdp"},
	{"homebase", "/var/www/lpm/homebase"},
	{"lpm-frontend", "/var/www/lpm/web-lpm-nextjs"},
	{"lpm-backend", "/var/www/lpm/lpm-backend"},
	{"fushpiapp", "/var/www/fushpi/fushpiapp"},
	{"syfa", "/var/www/syfa"},
	{"guest-house", "/var/www/guest-house"},
	{"koperasiapp", "/var/www/koperasi/koperasiapp"},
}

// BackupService reads backup status from disk + log.
type BackupService struct{}

func NewBackupService() *BackupService { return &BackupService{} }

// GetBackups returns status for each project.
func (s *BackupService) GetBackups(ctx context.Context) ([]BackupProject, error) {
	// best-effort drive check (timeout 3s, ignore error) — ponytail: no caching, add when Drive API needed
	func() {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = exec.CommandContext(cctx, "rclone", "lsjson", "gdrive:", "--max-depth", "3").Run()
	}()

	// collect daily dates sorted desc
	dates := listDates(dailyBase)

	out := make([]BackupProject, 0, len(backupProjects))
	for _, p := range backupProjects {
		bp := BackupProject{
			Name:      p.Name,
			Path:      p.Path,
			DriveLink: driveLink,
		}
		// size via du -sb (timeout 3s)
		if sz, err := dirSize(ctx, p.Path); err == nil {
			bp.Size = sz
			bp.SizeHuman = formatBytes(sz)
		}
		// find latest date where backup dir exists for this project
		last := ""
		for _, d := range dates {
			dir := filepath.Join(dailyBase, d, p.Name)
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
					last = d
					break
				}
			}
		}
		// also check hermes base if project is hermes-like (not in list, kept for completeness)
		bp.LastBackup = last

		// log tail for that date
		var tail []string
		var logJoined string
		if last != "" {
			logPath := filepath.Join(logDir, "projects-"+last+".log")
			tail, logJoined = readTail(logPath, 50)
			// filter to project-relevant lines if possible
			filtered := filterLogTail(tail, p.Name)
			if len(filtered) > 0 {
				tail = filtered
				logJoined = strings.Join(filtered, "\n")
			}
		} else {
			// no backup yet: try latest log file for context
			if latest := latestLogFile("projects"); latest != "" {
				tail, logJoined = readTail(latest, 50)
				filtered := filterLogTail(tail, p.Name)
				if len(filtered) > 0 {
					tail = filtered
					logJoined = strings.Join(filtered, "\n")
				}
			}
		}
		bp.LogTail = tail
		if bp.LogTail == nil {
			bp.LogTail = []string{}
		}

		// status
		switch {
		case last == "":
			bp.Status = "MISSING"
		case strings.Contains(strings.ToUpper(logJoined), "ERROR"):
			bp.Status = "FAILED"
		case strings.Contains(logJoined, "OK") || strings.Contains(strings.ToLower(logJoined), "sukses"):
			bp.Status = "OK"
		default:
			// file exists but log not conclusive — treat as OK
			bp.Status = "OK"
		}

		out = append(out, bp)
	}
	return out, nil
}

// GetBackupLog reads log file for given date and type.
// date: YYYY-MM-DD (optional, latest if empty). typ: projects|hermes (default projects).
func (s *BackupService) GetBackupLog(_ context.Context, date, typ string) (string, error) {
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "" {
		typ = "projects"
	}
	if typ != "projects" && typ != "hermes" {
		typ = "projects"
	}
	if date != "" {
		if !dateRe.MatchString(date) {
			return "", fmt.Errorf("format tanggal harus YYYY-MM-DD")
		}
		path := filepath.Join(logDir, typ+"-"+date+".log")
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "", ErrBackupLogNotFound
			}
			return "", err
		}
		return string(b), nil
	}
	// latest
	latest := latestLogFile(typ)
	if latest == "" {
		return "", ErrBackupLogNotFound
	}
	b, err := os.ReadFile(latest)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// helpers

func listDates(base string) []string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var dates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if dateRe.MatchString(e.Name()) {
			dates = append(dates, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates
}

func latestLogFile(typ string) string {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return ""
	}
	prefix := typ + "-"
	var dates []string
	m := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, ".log") {
			continue
		}
		d := strings.TrimSuffix(strings.TrimPrefix(n, prefix), ".log")
		if !dateRe.MatchString(d) {
			continue
		}
		dates = append(dates, d)
		m[d] = filepath.Join(logDir, n)
	}
	if len(dates) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return m[dates[0]]
}

func readTail(path string, n int) ([]string, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, ""
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, strings.Join(lines, "\n")
}

func filterLogTail(tail []string, project string) []string {
	var out []string
	tag := "[" + project + "]"
	for _, l := range tail {
		if strings.Contains(l, tag) || strings.Contains(l, project) {
			out = append(out, l)
		}
	}
	// if filter yields nothing, return original tail (so caller still sees context)
	if len(out) == 0 {
		return tail
	}
	// cap at 50
	if len(out) > 50 {
		out = out[len(out)-50:]
	}
	return out
}

func dirSize(ctx context.Context, path string) (int64, error) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "du", "-sb", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	// du -sb output: "<size>\t<path>"
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("du parse failed")
	}
	v, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.2f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
