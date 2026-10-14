package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
)

// ErrNotGitRepo is returned when the app's cwd is not a git working tree.
var ErrNotGitRepo = errors.New("bukan git repository")

// Deploy user: owns app trees + SSH keys for GitHub (panel may run as root for Docker).
const (
	gitDeployUser = "lpmobe"
	gitDeployHome = "/home/lpmobe"
	gitSSHCommand = "ssh -i /home/lpmobe/.ssh/id_ed25519 -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/home/lpmobe/.ssh/known_hosts"
)

// GitService runs git + deploy pipelines for PM2 and Docker apps.
//
// PM2:    git pull → npm install → npm run build → pm2 restart
// Docker: git pull → docker compose up -d --build
type GitService struct {
	term   *TerminalService
	pm2Cli *pm2.Client
}

func NewGitService(t *TerminalService, p *pm2.Client) *GitService {
	return &GitService{term: t, pm2Cli: p}
}

// PullResult carries the outcome of a git pull + deploy pipeline.
type PullResult struct {
	Branch    string `json:"branch"`
	OldCommit string `json:"oldCommit,omitempty"`
	NewCommit string `json:"newCommit,omitempty"`
	Changed   int    `json:"changedFiles"`
	Type      string `json:"type"`
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
}

type GitStatus struct {
	IsRepo   bool   `json:"isRepo"`
	Branch   string `json:"branch,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Remote   string `json:"remote,omitempty"`
	Dirty    bool   `json:"dirty"`
	AheadBy  int    `json:"aheadBy,omitempty"`
	BehindBy int    `json:"behindBy,omitempty"`
	Type     string `json:"type,omitempty"`
	Root     string `json:"root,omitempty"`
}

func (s *GitService) Status(ctx context.Context, appName string, claims *auth.Claims, w io.Writer) (GitStatus, error) {
	root, kind, err := s.resolveWorkRoot(ctx, appName, claims)
	if err != nil {
		return GitStatus{}, err
	}
	st := GitStatus{Type: kind, Root: root}

	if dirExists(root) && isGitRepo(root) {
		return s.statusFromHost(ctx, root, kind)
	}

	sess, oerr := s.term.Open(ctx, appName, claims)
	if oerr != nil {
		return st, nil
	}
	out2, err2 := s.runGit(ctx, sess, "rev-parse --git-dir", w)
	if err2 != nil || strings.TrimSpace(out2) == "" {
		return st, nil
	}
	return s.statusFromSession(ctx, sess, kind, sess.Root)
}

func (s *GitService) statusFromHost(ctx context.Context, root, kind string) (GitStatus, error) {
	st := GitStatus{IsRepo: true, Type: kind, Root: root}
	branch, _ := s.runHostGit(ctx, root, []string{"rev-parse", "--abbrev-ref", "HEAD"}, nil)
	commit, _ := s.runHostGit(ctx, root, []string{"rev-parse", "--short", "HEAD"}, nil)
	remote, _ := s.runHostGit(ctx, root, []string{"config", "--get", "remote.origin.url"}, nil)
	statusOut, _ := s.runHostGit(ctx, root, []string{"status", "--porcelain"}, nil)
	st.Branch = strings.TrimSpace(branch)
	st.Commit = strings.TrimSpace(commit)
	st.Remote = strings.TrimSpace(remote)
	st.Dirty = strings.TrimSpace(statusOut) != ""
	return st, nil
}

func (s *GitService) statusFromSession(ctx context.Context, sess *Session, kind, root string) (GitStatus, error) {
	st := GitStatus{IsRepo: true, Type: kind, Root: root}
	branch, _ := s.runGit(ctx, sess, "rev-parse --abbrev-ref HEAD", nil)
	commit, _ := s.runGit(ctx, sess, "rev-parse --short HEAD", nil)
	remote, _ := s.runGit(ctx, sess, "config --get remote.origin.url", nil)
	statusOut, _ := s.runGit(ctx, sess, "status --porcelain", nil)
	st.Branch = strings.TrimSpace(branch)
	st.Commit = strings.TrimSpace(commit)
	st.Remote = strings.TrimSpace(remote)
	st.Dirty = strings.TrimSpace(statusOut) != ""
	return st, nil
}

func (s *GitService) Branches(ctx context.Context, appName string, claims *auth.Claims) ([]string, error) {
	root, _, err := s.resolveWorkRoot(ctx, appName, claims)
	if err != nil {
		return nil, err
	}
	// Best-effort fetch so remote branches appear
	_, _ = s.runHostGit(ctx, root, []string{"fetch", "origin", "--prune"}, nil)

	out, err := s.runHostGit(ctx, root, []string{"branch", "-a", "--format=%(refname:short)"}, nil)
	if err != nil {
		sess, oerr := s.term.Open(ctx, appName, claims)
		if oerr != nil {
			return nil, err
		}
		out, err = s.runGit(ctx, sess, "branch -a --format=%(refname:short)", nil)
		if err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	var list []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if i := strings.Index(l, " -> "); i > 0 {
			l = l[:i]
		}
		l = normalizeBranch(l)
		if l == "" || l == "HEAD" || seen[l] {
			continue
		}
		seen[l] = true
		list = append(list, l)
	}
	// Prefer common names first
	prefer := []string{"main", "master", "develop", "staging", "production"}
	var ordered []string
	for _, p := range prefer {
		if seen[p] {
			ordered = append(ordered, p)
			delete(seen, p)
		}
	}
	for _, l := range list {
		if seen[l] {
			ordered = append(ordered, l)
		}
	}
	return ordered, nil
}

// Pull runs:
//   git fetch origin
//   git checkout <branch>
//   git pull origin <branch> --rebase --autostash
// then type-specific deploy (npm/pm2 or docker compose).
func (s *GitService) Pull(ctx context.Context, appName, branch string, claims *auth.Claims, w io.Writer) (PullResult, error) {
	write := func(format string, args ...any) {
		if w != nil {
			fmt.Fprintf(w, format, args...)
		}
	}

	root, kind, err := s.resolveWorkRoot(ctx, appName, claims)
	if err != nil {
		return PullResult{}, err
	}

	res := PullResult{Type: kind}
	write("\n=== Deploy: %s (%s) ===\n", appName, kind)
	write("Workdir: %s\n\n", root)

	useHost := dirExists(root) && isGitRepo(root)
	var sess *Session
	if !useHost {
		sess, err = s.term.Open(ctx, appName, claims)
		if err != nil {
			return res, err
		}
		root = sess.Root
		kind = sess.Kind
		res.Type = kind
	}

	// Normalize branch: "origin/main" -> "main", empty -> current HEAD
	branch = normalizeBranch(branch)
	if branch == "" {
		if useHost {
			b, _ := s.runHostGit(ctx, root, []string{"rev-parse", "--abbrev-ref", "HEAD"}, nil)
			branch = normalizeBranch(strings.TrimSpace(b))
		} else {
			b, _ := s.runGit(ctx, sess, "rev-parse --abbrev-ref HEAD", nil)
			branch = normalizeBranch(strings.TrimSpace(b))
		}
	}
	if branch == "" || branch == "HEAD" {
		branch = "main"
	}
	res.Branch = branch
	write("Target branch: origin/%s\n\n", branch)

	if useHost {
		old, _ := s.runHostGit(ctx, root, []string{"rev-parse", "--short", "HEAD"}, nil)
		res.OldCommit = strings.TrimSpace(old)

		write("$ git fetch origin --prune\n")
		if _, err := s.runHostGit(ctx, root, []string{"fetch", "origin", "--prune"}, w); err != nil {
			// fallback fetch --all
			write("WARN: fetch origin: %v — trying fetch --all\n", err)
			if _, err2 := s.runHostGit(ctx, root, []string{"fetch", "--all", "--prune"}, w); err2 != nil {
				return res, fmt.Errorf("git fetch gagal: %v / %v", err, err2)
			}
		}

		// Prefer tracking remote branch if it exists
		remoteRef := "origin/" + branch
		write("$ git checkout %s\n", branch)
		if _, err := s.runHostGit(ctx, root, []string{"checkout", branch}, w); err != nil {
			// create local branch from origin/<branch>
			write("local branch missing, checkout -B %s %s\n", branch, remoteRef)
			if _, err2 := s.runHostGit(ctx, root, []string{"checkout", "-B", branch, remoteRef}, w); err2 != nil {
				return res, fmt.Errorf("checkout branch %q gagal: %v / %v", branch, err, err2)
			}
		}

		// Explicit: git pull origin <branch>
		write("$ git pull origin %s --rebase --autostash\n", branch)
		if _, err := s.runHostGit(ctx, root, []string{"pull", "origin", branch, "--rebase", "--autostash"}, w); err != nil {
			write("rebase failed, trying plain: git pull origin %s\n", branch)
			if _, err2 := s.runHostGit(ctx, root, []string{"pull", "origin", branch, "--no-rebase"}, w); err2 != nil {
				return res, fmt.Errorf("git pull origin %s gagal: %v / %v", branch, err, err2)
			}
		}

		newC, _ := s.runHostGit(ctx, root, []string{"rev-parse", "--short", "HEAD"}, nil)
		res.NewCommit = strings.TrimSpace(newC)
		if res.OldCommit != res.NewCommit && res.NewCommit != "" {
			countOut, _ := s.runHostGit(ctx, root, []string{"rev-list", "--count", res.OldCommit + ".." + res.NewCommit}, nil)
			fmt.Sscanf(strings.TrimSpace(countOut), "%d", &res.Changed)
		}
	} else {
		old, _ := s.runGit(ctx, sess, "rev-parse --short HEAD", nil)
		res.OldCommit = strings.TrimSpace(old)
		write("$ git fetch origin --prune\n")
		_, _ = s.runGit(ctx, sess, "fetch origin --prune", w)
		write("$ git checkout %s\n", branch)
		if _, err := s.runGit(ctx, sess, fmt.Sprintf("checkout %q", branch), w); err != nil {
			if _, err2 := s.runGit(ctx, sess, fmt.Sprintf("checkout -B %q origin/%s", branch, branch), w); err2 != nil {
				return res, fmt.Errorf("checkout gagal: %v / %v", err, err2)
			}
		}
		write("$ git pull origin %s --rebase --autostash\n", branch)
		if _, err := s.runGit(ctx, sess, fmt.Sprintf("pull origin %s --rebase --autostash", branch), w); err != nil {
			if _, err2 := s.runGit(ctx, sess, fmt.Sprintf("pull origin %s --no-rebase", branch), w); err2 != nil {
				return res, fmt.Errorf("pull origin %s gagal: %v / %v", branch, err, err2)
			}
		}
		newC, _ := s.runGit(ctx, sess, "rev-parse --short HEAD", nil)
		res.NewCommit = strings.TrimSpace(newC)
	}

	write("\n=== git pull origin %s selesai: %s -> %s ===\n", branch, res.OldCommit, res.NewCommit)

	switch kind {
	case "pm2":
		if err := s.deployPM2(ctx, appName, root, w); err != nil {
			res.Message = err.Error()
			return res, err
		}
	case "docker":
		if err := s.deployDocker(ctx, appName, root, w); err != nil {
			res.Message = err.Error()
			return res, err
		}
	default:
		write("Unknown type %q — skip build/restart\n", kind)
	}

	res.Success = true
	res.Message = "deploy selesai"
	write("\n=== SELESAI: %s OK ===\n", appName)
	return res, nil
}

func (s *GitService) deployPM2(ctx context.Context, appName, root string, w io.Writer) error {
	write := func(format string, args ...any) {
		if w != nil {
			fmt.Fprintf(w, format, args...)
		}
	}
	work := root
	if !fileExists(filepath.Join(work, "package.json")) {
		if sub := findPackageJSONDir(root); sub != "" {
			work = sub
			write("package.json found in: %s\n", work)
		}
	}

	if fileExists(filepath.Join(work, "package.json")) {
		write("\n$ npm install\n")
		if err := s.runHostCmd(ctx, work, []string{"npm", "install"}, w); err != nil {
			return fmt.Errorf("npm install gagal: %w", err)
		}
		write("\n$ npm run build\n")
		if err := s.runHostCmd(ctx, work, []string{"npm", "run", "build"}, w); err != nil {
			write("WARN: npm run build gagal (mungkin tidak ada script build): %v\n", err)
		}
	} else {
		write("Tidak ada package.json — skip npm install/build\n")
	}

	write("\n$ pm2 restart %s\n", appName)
	if s.pm2Cli != nil {
		if err := s.pm2Cli.Action(ctx, "restart", appName); err != nil {
			if err2 := s.runHostCmd(ctx, work, []string{"pm2", "restart", appName}, w); err2 != nil {
				return fmt.Errorf("pm2 restart gagal: %v / %v", err, err2)
			}
		} else {
			write("pm2 restart OK\n")
		}
	} else if err := s.runHostCmd(ctx, work, []string{"pm2", "restart", appName}, w); err != nil {
		return fmt.Errorf("pm2 restart gagal: %w", err)
	}
	return nil
}

func (s *GitService) deployDocker(ctx context.Context, appName, root string, w io.Writer) error {
	write := func(format string, args ...any) {
		if w != nil {
			fmt.Fprintf(w, format, args...)
		}
	}

	composeDir := root
	if !hasComposeFile(composeDir) {
		if d := findComposeDir(root); d != "" {
			composeDir = d
		}
	}
	if !hasComposeFile(composeDir) {
		return fmt.Errorf("docker-compose.yml tidak ditemukan di %s", root)
	}

	write("\nCompose dir: %s\n", composeDir)
	write("$ docker compose up -d --build\n")
	if err := s.runHostCmd(ctx, composeDir, []string{"docker", "compose", "up", "-d", "--build"}, w); err != nil {
		write("docker compose gagal, coba docker-compose...\n")
		if err2 := s.runHostCmd(ctx, composeDir, []string{"docker-compose", "up", "-d", "--build"}, w); err2 != nil {
			return fmt.Errorf("docker compose up gagal: %v / %v", err, err2)
		}
	}
	write("docker compose up -d --build OK (%s)\n", appName)
	return nil
}

func (s *GitService) resolveWorkRoot(ctx context.Context, appName string, claims *auth.Claims) (string, string, error) {
	sess, err := s.term.Open(ctx, appName, claims)
	if err != nil {
		return "", "", err
	}
	if sess.Kind == "pm2" {
		return sess.Root, "pm2", nil
	}

	dc := s.term.Docker()
	if dc != nil && sess.ContID != "" {
		if ci, err := dc.ComposeInfo(ctx, sess.ContID); err == nil && ci != nil {
			if ci.WorkingDir != "" && dirExists(ci.WorkingDir) {
				return ci.WorkingDir, "docker", nil
			}
		}
		if binds, err := dc.HostBinds(ctx, sess.ContID); err == nil {
			for _, b := range binds {
				if isGitRepo(b) || hasComposeFile(b) {
					return b, "docker", nil
				}
				if isGitRepo(filepath.Dir(b)) || hasComposeFile(filepath.Dir(b)) {
					return filepath.Dir(b), "docker", nil
				}
			}
			if len(binds) > 0 && dirExists(binds[0]) {
				return binds[0], "docker", nil
			}
		}
	}
	return sess.Root, "docker", nil
}

func (s *GitService) runHostGit(ctx context.Context, dir string, args []string, w io.Writer) (string, error) {
	// Run as deploy user (lpmobe) so SSH keys under ~/.ssh work for git@github.com.
	// Panel service often runs as root (for docker.sock) which has no GitHub key.
	full := []string{
		"-u", gitDeployUser,
		"-H",
		"--",
		"git",
		"-c", "safe.directory=*",
		"-c", "safe.directory=" + dir,
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "sudo", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"HOME="+gitDeployHome,
		"USER="+gitDeployUser,
		"LANG=C",
		"GIT_SSH_COMMAND="+gitSSHCommand,
	)
	var buf bytes.Buffer
	if w != nil {
		cmd.Stdout = io.MultiWriter(&buf, w)
		cmd.Stderr = io.MultiWriter(&buf, w)
	} else {
		cmd.Stdout = &buf
		cmd.Stderr = &buf
	}
	err := cmd.Run()
	return buf.String(), err
}

func (s *GitService) runHostCmd(ctx context.Context, dir string, args []string, w io.Writer) error {
	if len(args) == 0 {
		return errors.New("empty command")
	}
	// Docker needs root (socket). npm/pm2 should run as deploy user.
	asDeployUser := true
	if args[0] == "docker" || args[0] == "docker-compose" {
		asDeployUser = false
	}

	var cmd *exec.Cmd
	path := "/home/lpmobe/.nvm/versions/node/v25.2.1/bin:/usr/local/bin:/usr/bin:/bin"
	env := append(os.Environ(),
		"PATH="+path+":"+os.Getenv("PATH"),
		"HOME="+gitDeployHome,
		"USER="+gitDeployUser,
		"PM2_HOME="+gitDeployHome+"/.pm2",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND="+gitSSHCommand,
		"LANG=C",
	)

	if asDeployUser {
		full := []string{"-u", gitDeployUser, "-H", "--"}
		full = append(full, args...)
		cmd = exec.CommandContext(ctx, "sudo", full...)
	} else {
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
	}
	cmd.Dir = dir
	cmd.Env = env
	if w != nil {
		cmd.Stdout = w
		cmd.Stderr = w
	}
	return cmd.Run()
}

func (s *GitService) runGit(ctx context.Context, sess *Session, args string, w io.Writer) (string, error) {
	switch sess.Kind {
	case "docker":
		return s.runGitDocker(ctx, sess, args, w)
	default:
		return s.runGitHost(ctx, sess, args, w)
	}
}

func (s *GitService) runGitHost(ctx context.Context, sess *Session, args string, w io.Writer) (string, error) {
	return s.runHostGit(ctx, sess.Root, splitGitArgs(args), w)
}

func (s *GitService) runGitDocker(ctx context.Context, sess *Session, args string, w io.Writer) (string, error) {
	dc := s.term.Docker()
	if dc == nil {
		return "", errors.New("docker client tidak tersedia")
	}
	// safe.directory inside container too
	shellCmd := fmt.Sprintf(
		"cd %q && git -c safe.directory=* -c safe.directory=%q %s",
		sess.Root, sess.Root, args,
	)
	out, err := dc.Exec(ctx, sess.ContID, shellCmd)
	if w != nil && out != "" {
		_, _ = io.WriteString(w, out)
	}
	return out, err
}

func normalizeBranch(b string) string {
	b = strings.TrimSpace(b)
	b = strings.TrimPrefix(b, "refs/heads/")
	b = strings.TrimPrefix(b, "refs/remotes/")
	b = strings.TrimPrefix(b, "origin/")
	return b
}

func splitGitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func isGitRepo(p string) bool {
	if !dirExists(p) {
		return false
	}
	return dirExists(filepath.Join(p, ".git")) || fileExists(filepath.Join(p, ".git"))
}

func hasComposeFile(dir string) bool {
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

func findComposeDir(start string) string {
	cur := start
	for i := 0; i < 6; i++ {
		if hasComposeFile(cur) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	entries, err := os.ReadDir(start)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			p := filepath.Join(start, e.Name())
			if hasComposeFile(p) {
				return p
			}
		}
	}
	return ""
}

func findPackageJSONDir(start string) string {
	if fileExists(filepath.Join(start, "package.json")) {
		return start
	}
	entries, err := os.ReadDir(start)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "node_modules" || name == ".git" {
			continue
		}
		p := filepath.Join(start, name)
		if fileExists(filepath.Join(p, "package.json")) {
			return p
		}
	}
	return ""
}

type GitDiff struct {
	Files   []string `json:"files"`
	Commits []string `json:"commits"`
}

func (s *GitService) Diff(ctx context.Context, appName string, claims *auth.Claims) (GitDiff, error) {
	root, _, err := s.resolveWorkRoot(ctx, appName, claims)
	if err != nil {
		return GitDiff{}, err
	}
	d := GitDiff{}
	// fetch first
	_, _ = s.runHostGit(ctx, root, []string{"fetch", "origin", "--prune"}, nil)
	out, _ := s.runHostGit(ctx, root, []string{"diff", "--name-status", "HEAD..origin/HEAD"}, nil)
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			d.Files = append(d.Files, l)
		}
	}
	logOut, _ := s.runHostGit(ctx, root, []string{"log", "--oneline", "HEAD..origin/HEAD", "--max-count=50"}, nil)
	for _, l := range strings.Split(strings.TrimSpace(logOut), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			d.Commits = append(d.Commits, l)
		}
	}
	if d.Files == nil {
		d.Files = []string{}
	}
	if d.Commits == nil {
		d.Commits = []string{}
	}
	return d, nil
}
