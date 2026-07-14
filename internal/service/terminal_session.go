package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrTerminalForbiddenChar = errors.New("command mengandung karakter terlarang: ';' '|' '>' '<' '`' '$' '&' atau redirection")
	ErrTerminalEmptyCmd      = errors.New("command kosong")
	ErrTerminalPathEscape    = errors.New("command mencoba mengakses path di luar root yang diizinkan")
	ErrTerminalTooLong       = errors.New("command terlalu panjang")
	ErrTerminalNotAllowed    = errors.New("anda tidak memiliki akses ke aplikasi ini")
)

// Disallowed chars: ; | > < ` $ & and CR/LF (command separator, backgrounding, subshell, redirection).
var disallowedChar = regexp.MustCompile(`[;|<>\` + "`" + `&$]|[\r\n]`)

const maxCmdLen = 4096

// Session is a pre-flight validated handle to a per-app terminal context.
// All commands run with their CWD forced to the app's root path.
type Session struct {
	App     string // app name
	Root    string // absolute root path (host fs)
	Kind    string // "pm2" | "docker"
	ContID  string // for docker
}

// validateCmd runs pre-flight checks on a command string:
//   - length
//   - disallowed characters
//   - every absolute-looking path stays under root
func (s *Session) validateCmd(raw string) error {
	cmd := strings.TrimSpace(raw)
	if cmd == "" {
		return ErrTerminalEmptyCmd
	}
	if len(cmd) > maxCmdLen {
		return ErrTerminalTooLong
	}
	if disallowedChar.MatchString(cmd) {
		return ErrTerminalForbiddenChar
	}
	if err := s.scanPaths(cmd); err != nil {
		return err
	}
	return nil
}

// pathToken matches tokens that look like absolute paths inside a command line.
// It picks up /foo, /foo/bar.txt, but not flags like -x.
var pathToken = regexp.MustCompile(`(?:^|[\s"'])(/[^\s"'|;<>$` + "`" + `&]*)`)

func (s *Session) scanPaths(cmd string) error {
	matches := pathToken.FindAllStringSubmatch(cmd, -1)
	for _, m := range matches {
		p := m[1]
		if p == "/" {
			return ErrTerminalPathEscape
		}
		cleaned := filepath.Clean(p)
		if !strings.HasPrefix(cleaned, s.Root) {
			return ErrTerminalPathEscape
		}
	}
	return nil
}

// ExecHost runs a single command on the host with CWD forced to root.
// Output is streamed to w. Returns exit code (or -1, err on infrastructure failure).
func (s *Session) ExecHost(ctx context.Context, raw string, w io.Writer) (int, error) {
	if err := s.validateCmd(raw); err != nil {
		return -1, err
	}
	wrapped := fmt.Sprintf("cd %q && %s", s.Root, raw)
	cmd := exec.CommandContext(ctx, "bash", "-c", wrapped)
	cmd.Env = append(os.Environ(), "PWD="+s.Root, "HOME="+s.Root)
	cmd.Dir = s.Root
	return streamCmd(ctx, cmd, w)
}

func streamCmd(ctx context.Context, cmd *exec.Cmd, w io.Writer) (int, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(w, stdout); done <- struct{}{} }()
	go func() { _, _ = io.Copy(w, stderr); done <- struct{}{} }()
	<-done
	<-done
	if err := cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}
