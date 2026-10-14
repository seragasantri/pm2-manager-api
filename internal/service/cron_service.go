package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os/exec"
	"strings"
	"time"
)

var (
	ErrCronNotFound    = errors.New("cron job tidak ditemukan")
	ErrCronInvalidSch  = errors.New("jadwal cron tidak valid (harus 5 field)")
	ErrCronCmdRequired = errors.New("command diperlukan")
)

// CronJob represents a single crontab entry.
type CronJob struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Command  string     `json:"command"`
	Schedule string     `json:"schedule"`
	Enabled  bool       `json:"enabled"`
	LastRun  *time.Time `json:"lastRun,omitempty"`
	NextRun  *time.Time `json:"nextRun,omitempty"`
}

// CronService manages crontab for user lpmobe.
type CronService struct{}

func NewCronService() *CronService { return &CronService{} }

// GetCronJobs reads `crontab -l` and parses entries.
func (s *CronService) GetCronJobs(ctx context.Context) ([]CronJob, error) {
	lines, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	jobs := make([]CronJob, 0)
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		enabled := !strings.HasPrefix(trimmed, "#")
		content := trimmed
		if !enabled {
			// strip leading '#'
			content = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			// handle "# # ..." or "#  #"
			content = strings.TrimSpace(strings.TrimPrefix(content, "#"))
		}
		// skip pure comments that don't look like cron
		fields := strings.Fields(content)
		if len(fields) < 6 {
			continue
		}
		schedule := strings.Join(fields[0:5], " ")
		if !looksLikeSchedule(schedule) {
			continue
		}
		remaining := strings.Join(fields[5:], " ")
		cmd, name := splitCommandName(remaining)
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		id := cronID(schedule, cmd, name)
		nr := computeNextRun(schedule)
		j := CronJob{
			ID:       id,
			Name:     name,
			Command:  cmd,
			Schedule: schedule,
			Enabled:  enabled,
			NextRun:  nr,
		}
		jobs = append(jobs, j)
	}
	// deduplicate IDs (duplicate schedule+command): keep first, suffix others
	seen := map[string]int{}
	for i := range jobs {
		base := jobs[i].ID
		if c, ok := seen[base]; ok {
			seen[base] = c + 1
			jobs[i].ID = fmt.Sprintf("%s-%d", base, c+1)
		} else {
			seen[base] = 1
		}
	}
	if jobs == nil {
		jobs = []CronJob{}
	}
	return jobs, nil
}

// CreateCronJob appends a new entry.
func (s *CronService) CreateCronJob(ctx context.Context, command, schedule, name string) (*CronJob, error) {
	if strings.TrimSpace(command) == "" {
		return nil, ErrCronCmdRequired
	}
	schedule = strings.TrimSpace(schedule)
	if err := validateSchedule(schedule); err != nil {
		return nil, err
	}
	lines, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	newLine := buildCronLine(schedule, command, name, true)
	lines = append(lines, newLine)
	if err := s.writeCrontab(ctx, lines); err != nil {
		return nil, err
	}
	// return the created job (ID derived same way)
	nr := computeNextRun(schedule)
	return &CronJob{
		ID:       cronID(schedule, strings.TrimSpace(command), strings.TrimSpace(name)),
		Name:     strings.TrimSpace(name),
		Command:  strings.TrimSpace(command),
		Schedule: schedule,
		Enabled:  true,
		NextRun:  nr,
	}, nil
}

// UpdateCronJob updates entry matched by id.
func (s *CronService) UpdateCronJob(ctx context.Context, id, command, schedule, name string) (*CronJob, error) {
	schedule = strings.TrimSpace(schedule)
	if schedule != "" {
		if err := validateSchedule(schedule); err != nil {
			return nil, err
		}
	}
	lines, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	idx, job, err := s.findByID(lines, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(command) != "" {
		job.Command = strings.TrimSpace(command)
	}
	if schedule != "" {
		job.Schedule = schedule
	}
	// name can be cleared explicitly: if caller passes name param we use it (even empty means clear)
	// We distinguish: caller should pass name as provided; if they want to keep existing, they pass job.Name.
	// Here we update if name is not the sentinel "__keep__" – but our handler always passes body name, so we overwrite.
	job.Name = strings.TrimSpace(name)
	newLine := buildCronLine(job.Schedule, job.Command, job.Name, job.Enabled)
	lines[idx] = newLine
	if err := s.writeCrontab(ctx, lines); err != nil {
		return nil, err
	}
	nr := computeNextRun(job.Schedule)
	job.NextRun = nr
	return job, nil
}

// DeleteCronJob removes entry by id.
func (s *CronService) DeleteCronJob(ctx context.Context, id string) error {
	lines, err := s.readCrontab(ctx)
	if err != nil {
		return err
	}
	idx, _, err := s.findByID(lines, id)
	if err != nil {
		return err
	}
	// remove idx
	lines = append(lines[:idx], lines[idx+1:]...)
	return s.writeCrontab(ctx, lines)
}

// ToggleCronJob enables/disables job.
func (s *CronService) ToggleCronJob(ctx context.Context, id string, enabled bool) (*CronJob, error) {
	lines, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	idx, job, err := s.findByID(lines, id)
	if err != nil {
		return nil, err
	}
	if job.Enabled == enabled {
		nr := computeNextRun(job.Schedule)
		job.NextRun = nr
		return job, nil
	}
	job.Enabled = enabled
	lines[idx] = buildCronLine(job.Schedule, job.Command, job.Name, enabled)
	if err := s.writeCrontab(ctx, lines); err != nil {
		return nil, err
	}
	nr := computeNextRun(job.Schedule)
	job.NextRun = nr
	return job, nil
}

// ---- helpers ----

func (s *CronService) readCrontab(ctx context.Context) ([]string, error) {
	// try with -u lpmobe first, fallback to plain
	out, err := s.crontabList(ctx, true)
	if err != nil {
		// if error contains "must be privileged" or "user unknown", try without -u
		if strings.Contains(string(out), "must be privileged") || strings.Contains(string(out), "unknown user") {
			out2, err2 := s.crontabList(ctx, false)
			if err2 != nil {
				if isNoCrontab(out2) {
					return []string{}, nil
				}
				return nil, fmt.Errorf("crontab -l: %s: %w", strings.TrimSpace(string(out2)), err2)
			}
			out = out2
		} else if isNoCrontab(out) {
			return []string{}, nil
		} else if err != nil {
			// check if output indicates empty
			if isNoCrontab(out) {
				return []string{}, nil
			}
			return nil, fmt.Errorf("crontab -l: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return []string{}, nil
	}
	// keep original lines (including comments/disabled) for write-back
	// but we need raw lines including empty? For simplicity split by \n
	rawLines := strings.Split(string(out), "\n")
	// trim trailing empty caused by final newline
	if len(rawLines) > 0 && strings.TrimSpace(rawLines[len(rawLines)-1]) == "" && len(rawLines) > 1 {
		// keep as is but don't add extra empty at end for parsing; write will normalize
	}
	return rawLines, nil
}

func (s *CronService) crontabList(ctx context.Context, withUser bool) ([]byte, error) {
	var cmd *exec.Cmd
	if withUser {
		cmd = exec.CommandContext(ctx, "crontab", "-u", "lpmobe", "-l")
	} else {
		cmd = exec.CommandContext(ctx, "crontab", "-l")
	}
	out, err := cmd.CombinedOutput()
	return out, err
}

func isNoCrontab(out []byte) bool {
	s := strings.ToLower(strings.TrimSpace(string(out)))
	return strings.Contains(s, "no crontab for")
}

func (s *CronService) writeCrontab(ctx context.Context, lines []string) error {
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n")
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
	}
	// try with -u lpmobe first
	err := s.crontabWrite(ctx, content, true)
	if err != nil && strings.Contains(err.Error(), "must be privileged") {
		return s.crontabWrite(ctx, content, false)
	}
	return err
}

func (s *CronService) crontabWrite(ctx context.Context, content string, withUser bool) error {
	var cmd *exec.Cmd
	if withUser {
		cmd = exec.CommandContext(ctx, "crontab", "-u", "lpmobe", "-")
	} else {
		cmd = exec.CommandContext(ctx, "crontab", "-")
	}
	cmd.Stdin = strings.NewReader(content)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("crontab -: %s: %w", msg, err)
	}
	return nil
}

func (s *CronService) findByID(lines []string, id string) (int, *CronJob, error) {
	// iterate with same logic as GetCronJobs to match ID
	// we need to map ID to raw line index
	// use deduplication counter
	seen := map[string]int{}
	for idx, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		enabled := !strings.HasPrefix(trimmed, "#")
		content := trimmed
		if !enabled {
			content = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			content = strings.TrimSpace(strings.TrimPrefix(content, "#"))
		}
		fields := strings.Fields(content)
		if len(fields) < 6 {
			continue
		}
		schedule := strings.Join(fields[0:5], " ")
		if !looksLikeSchedule(schedule) {
			continue
		}
		remaining := strings.Join(fields[5:], " ")
		cmd, name := splitCommandName(remaining)
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		base := cronID(schedule, cmd, name)
		count := seen[base]
		curID := base
		if count > 0 {
			curID = fmt.Sprintf("%s-%d", base, count+1)
		}
		seen[base] = count + 1
		if curID == id {
			j := &CronJob{
				ID:       curID,
				Name:     name,
				Command:  cmd,
				Schedule: schedule,
				Enabled:  enabled,
			}
			return idx, j, nil
		}
	}
	return -1, nil, ErrCronNotFound
}

func cronID(schedule, command, name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(schedule))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(command))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%08x", h.Sum32())
}

func buildCronLine(schedule, command, name string, enabled bool) string {
	command = strings.TrimSpace(command)
	name = strings.TrimSpace(name)
	line := schedule + " " + command
	if name != "" {
		line += " # " + name
	}
	if !enabled {
		line = "# " + line
	}
	return line
}

func splitCommandName(remaining string) (string, string) {
	// trailing " # name" -> name
	if idx := strings.LastIndex(remaining, " # "); idx != -1 {
		cmd := strings.TrimSpace(remaining[:idx])
		name := strings.TrimSpace(remaining[idx+3:])
		return cmd, name
	}
	return strings.TrimSpace(remaining), ""
}

func validateSchedule(s string) error {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return ErrCronInvalidSch
	}
	for _, f := range fields {
		if strings.TrimSpace(f) == "" {
			return ErrCronInvalidSch
		}
	}
	if !looksLikeSchedule(s) {
		return ErrCronInvalidSch
	}
	return nil
}

func looksLikeSchedule(s string) bool {
	// very light check: 5 fields, each contains allowed chars
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return false
	}
	for _, f := range fields {
		if f == "" {
			return false
		}
		for _, ch := range f {
			if !(ch >= '0' && ch <= '9' || ch == '*' || ch == '/' || ch == '-' || ch == ',' || ch == '?') {
				return false
			}
		}
	}
	return true
}

// computeNextRun does coarse next-run estimation without external cron parser.
// ponytail: ceiling is minute-level brute force (max ~1 year scan); upgrade: use github.com/robfig/cron/v3 parser.
func computeNextRun(schedule string) *time.Time {
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return nil
	}
	now := time.Now().Truncate(time.Minute).Add(time.Minute)
	// brute force up to 366 days
	for i := 0; i < 366*24*60; i++ {
		t := now.Add(time.Duration(i) * time.Minute)
		if matchesSchedule(t, fields) {
			tt := t
			return &tt
		}
	}
	return nil
}

func matchesSchedule(t time.Time, fields []string) bool {
	if !fieldMatches(fields[0], t.Minute(), 0, 59) {
		return false
	}
	if !fieldMatches(fields[1], t.Hour(), 0, 23) {
		return false
	}
	if !fieldMatches(fields[2], t.Day(), 1, 31) {
		return false
	}
	if !fieldMatches(fields[3], int(t.Month()), 1, 12) {
		return false
	}
	// weekday: Sunday 0 and 7 both mean Sunday
	wd := int(t.Weekday())
	if !fieldMatches(fields[4], wd, 0, 7) {
		// also try 7->0 mapping
		if wd == 0 {
			if !fieldMatches(fields[4], 7, 0, 7) {
				return false
			}
		} else {
			return false
		}
	}
	return true
}

func fieldMatches(expr string, val, min, max int) bool {
	if expr == "*" {
		return true
	}
	// handle comma list and ranges and steps
	parts := strings.Split(expr, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// step: "*/5" or "1-10/2"
		if strings.Contains(p, "/") {
			sp := strings.SplitN(p, "/", 2)
			base := sp[0]
			stepStr := sp[1]
			step := 0
			fmt.Sscan(stepStr, &step)
			if step <= 0 {
				continue
			}
			if base == "*" {
				if val%step == 0 {
					return true
				}
				continue
			}
			// range with step
			if strings.Contains(base, "-") {
				bounds := strings.SplitN(base, "-", 2)
				var lo, hi int
				fmt.Sscan(bounds[0], &lo)
				fmt.Sscan(bounds[1], &hi)
				if val >= lo && val <= hi && (val-lo)%step == 0 {
					return true
				}
			}
			continue
		}
		if strings.Contains(p, "-") {
			bounds := strings.SplitN(p, "-", 2)
			var lo, hi int
			fmt.Sscan(bounds[0], &lo)
			fmt.Sscan(bounds[1], &hi)
			if val >= lo && val <= hi {
				return true
			}
			continue
		}
		var n int
		if _, err := fmt.Sscan(p, &n); err == nil {
			// handle 7 as Sunday alias
			if max == 7 && n == 7 && val == 0 {
				return true
			}
			if max == 7 && n == 0 && val == 7 {
				// not needed; wd 0 alias already
			}
			if n == val {
				return true
			}
			// weekday 7 alias
			if max == 7 && val == 0 && n == 7 {
				return true
			}
		}
		if p == "?" {
			return true
		}
		_ = min
	}
	return false
}
