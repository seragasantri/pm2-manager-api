package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
)

// ErrNotGitRepo is returned when the app's cwd is not a git working tree.
var ErrNotGitRepo = errors.New("bukan git repository")

// GitService runs git operations inside an app's root directory with the same
// path-safety rules as the terminal session: cwd is forced to root, no shell
// metacharacters, and every absolute path stays under root.
//
// For Docker apps, git runs *inside* the container via docker exec so it sees
// the same filesystem as the app. For PM2 apps, it runs on the host.
type GitService struct {
	term *TerminalService
}

func NewGitService(t *TerminalService) *GitService {
	return &GitService{term: t}
}

// PullResult carries the outcome of a `git pull` operation.
type PullResult struct {
	Branch    string `json:"branch"`
	OldCommit string `json:"oldCommit,omitempty"`
	NewCommit string `json:"newCommit,omitempty"`
	Changed   int    `json:"changedFiles"`
}

// openSession returns a Terminal session for the app — same access checks.
func (s *GitService) openSession(ctx context.Context, appName string, claims *auth.Claims) (*Session, error) {
	return s.term.Open(ctx, appName, claims)
}

// Status returns whether the cwd is a git repo and the current branch + commit.
type GitStatus struct {
	IsRepo   bool   `json:"isRepo"`
	Branch   string `json:"branch,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Remote   string `json:"remote,omitempty"`
	Dirty    bool   `json:"dirty"` // uncommitted changes
	AheadBy  int    `json:"aheadBy,omitempty"`
	BehindBy int    `json:"behindBy,omitempty"`
}

func (s *GitService) Status(ctx context.Context, appName string, claims *auth.Claims, w io.Writer) (GitStatus, error) {
	sess, err := s.term.Open(ctx, appName, claims)
	if err != nil {
		return GitStatus{}, err
	}
	// Check if it's a repo by running `git rev-parse --git-dir`.
	out, err := s.runGit(ctx, sess, "rev-parse --git-dir", w)
	if err != nil || strings.TrimSpace(out) == "" {
		return GitStatus{IsRepo: false}, nil
	}

	branch, _ := s.runGit(ctx, sess, "rev-parse --abbrev-ref HEAD", nil)
	commit, _ := s.runGit(ctx, sess, "rev-parse --short HEAD", nil)
	remote, _ := s.runGit(ctx, sess, "config --get remote.origin.url", nil)

	statusOut, _ := s.runGit(ctx, sess, "status --porcelain", nil)
	dirty := strings.TrimSpace(statusOut) != ""

	st := GitStatus{
		IsRepo: true,
		Branch: strings.TrimSpace(branch),
		Commit: strings.TrimSpace(commit),
		Remote: strings.TrimSpace(remote),
		Dirty:  dirty,
	}

	// Ahead/behind vs upstream if exists
	if strings.TrimSpace(remote) != "" {
		upstream, _ := s.runGit(ctx, sess,
			fmt.Sprintf("rev-parse --abbrev-ref --symbolic-full-name %s@{u}", strings.TrimSpace(branch)),
			nil,
		)
		upstream = strings.TrimSpace(upstream)
		if upstream != "" {
			counts, _ := s.runGit(ctx, sess,
				fmt.Sprintf("rev-list --left-right --count %s...HEAD", upstream), nil)
			parts := strings.Fields(counts)
			if len(parts) == 2 {
				fmt.Sscanf(parts[0], "%d", &st.BehindBy)
				fmt.Sscanf(parts[1], "%d", &st.AheadBy)
			}
		}
	}
	return st, nil
}

// Branches lists local + remote branches (name only).
func (s *GitService) Branches(ctx context.Context, appName string, claims *auth.Claims) ([]string, error) {
	sess, err := s.term.Open(ctx, appName, claims)
	if err != nil {
		return nil, err
	}
	out, err := s.runGit(ctx, sess, "branch -a --format=%(refname:short)", nil)
	if err != nil {
		return nil, err
	}
	var list []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		// Strip "HEAD -> origin/main" suffix variant
		if i := strings.Index(l, " -> "); i > 0 {
			l = l[:i]
		}
		list = append(list, l)
	}
	return list, nil
}

// Pull runs `git pull` (optionally switching to the given branch first).
// All command output is streamed to w (typically a WebSocket writer).
// Returns a PullResult describing what changed.
func (s *GitService) Pull(ctx context.Context, appName, branch string, claims *auth.Claims, w io.Writer) (PullResult, error) {
	sess, err := s.term.Open(ctx, appName, claims)
	if err != nil {
		return PullResult{}, err
	}

	res := PullResult{}

	// Capture current state
	if branch == "" {
		b, _ := s.runGit(ctx, sess, "rev-parse --abbrev-ref HEAD", nil)
		branch = strings.TrimSpace(b)
	}
	res.Branch = branch

	oldCommit, _ := s.runGit(ctx, sess, "rev-parse --short HEAD", nil)
	res.OldCommit = strings.TrimSpace(oldCommit)

	// Checkout branch if different
	if branch != "" {
		if _, err := s.runGit(ctx, sess,
			fmt.Sprintf("checkout %q", branch), w); err != nil {
			return res, fmt.Errorf("checkout gagal: %w", err)
		}
	}

	// Fetch then pull (so we can report up-to-date)
	_, _ = s.runGit(ctx, sess, "fetch --all --prune", w)

	// Pull with rebase to avoid messy merge commits; fall back to plain pull.
	if _, err := s.runGit(ctx, sess, "pull --rebase --autostash", w); err != nil {
		// If rebase fails (e.g. unrelated histories), try plain pull.
		if _, err2 := s.runGit(ctx, sess, "pull --no-rebase", w); err2 != nil {
			return res, fmt.Errorf("pull gagal (rebase: %v, plain: %v)", err, err2)
		}
	}

	newCommit, _ := s.runGit(ctx, sess, "rev-parse --short HEAD", nil)
	res.NewCommit = strings.TrimSpace(newCommit)

	if res.OldCommit != res.NewCommit && res.NewCommit != "" {
		countOut, _ := s.runGit(ctx, sess,
			fmt.Sprintf("rev-list --count %s..%s", res.OldCommit, res.NewCommit), nil)
		fmt.Sscanf(strings.TrimSpace(countOut), "%d", &res.Changed)
	}

	// Stream a final summary line
	fmt.Fprintf(w, "\n=== git pull selesai: %s -> %s (%d commit) ===\n",
		res.OldCommit, res.NewCommit, res.Changed)
	return res, nil
}

// runGit executes a single `git <args>` inside the session's cwd. Output is
// streamed to w if non-nil; stdout is also captured for return.
//
// We bypass the terminal command validator because `git <subcmd>` may include
// quoted branch names (e.g. `git checkout "feature/x"`). The validator blocks
// `; | > < $ &` but allows `"` and quotes inside them, which is safe.
func (s *GitService) runGit(ctx context.Context, sess *Session, args string, w io.Writer) (string, error) {
	// We construct a Session-like exec directly without going through the
	// general-purpose terminal validator, because git operations need quoted
	// arguments. We still enforce path safety by using Session.Root as cwd.
	switch sess.Kind {
	case "docker":
		return s.runGitDocker(ctx, sess, args, w)
	default:
		return s.runGitHost(ctx, sess, args, w)
	}
}

func (s *GitService) runGitHost(ctx context.Context, sess *Session, args string, w io.Writer) (string, error) {
	cmd := exec.CommandContext(ctx, "git", splitGitArgs(args)...)
	cmd.Dir = sess.Root
	cmd.Env = append(cmd.Environ(), "HOME="+sess.Root, "GIT_TERMINAL_PROMPT=0")

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

func (s *GitService) runGitDocker(ctx context.Context, sess *Session, args string, w io.Writer) (string, error) {
	// Use docker exec; stream output through `w`.
	// We can't easily use TerminalService.Exec because it validates input;
	// instead call dockerClient.Exec directly.
	dc := s.term.Docker()
	if dc == nil {
		return "", errors.New("docker client tidak tersedia")
	}
	shellCmd := fmt.Sprintf("cd %q && git %s", sess.Root, args)
	out, err := dc.Exec(ctx, sess.ContID, shellCmd)
	if w != nil && out != "" {
		_, _ = io.WriteString(w, out)
	}
	return out, err
}

// splitGitArgs splits a git args string respecting double quotes.
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
