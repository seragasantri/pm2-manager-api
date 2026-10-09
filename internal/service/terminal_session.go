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
	"sync"
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
	App     string // app name ("*" = terminal umum, akses penuh server)
	Root    string // absolute root path (host fs)
	Kind    string // "pm2" | "docker" | "host"
	ContID  string // for docker
	General bool   // true = terminal umum (tanpa filter keamanan ketat)

	mu  sync.Mutex // menjaga Cwd sesi umum (WS + REST bisa jalan bersamaan)
	Cwd string     // folder kerja sesi umum saat ini (diingat antar perintah)
	Prev string    // folder sebelumnya (untuk "cd -")
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
	if s.General {
		return s.execGeneral(ctx, raw, w)
	}
	if err := s.validateCmd(raw); err != nil {
		return -1, err
	}
	wrapped := fmt.Sprintf("cd %q && %s", s.Root, raw)
	cmd := exec.CommandContext(ctx, "bash", "-c", wrapped)
	cmd.Env = append(os.Environ(), "PWD="+s.Root, "HOME="+s.Root)
	cmd.Dir = s.Root
	return streamCmd(ctx, cmd, w)
}

// execGeneral menjalankan perintah TANPA filter keamanan ketat (mode SSH).
// Hanya untuk sesi terminal umum (superadmin). Sesi MENGINGAT folder kerja:
// perintah "cd X" murni memindahkan posisi (tanpa menjalankan apa pun),
// perintah lain dijalankan di folder terakhir. Batas yang tersisa: panjang
// maksimal perintah.
func (s *Session) execGeneral(ctx context.Context, raw string, w io.Writer) (int, error) {
	cmd := strings.TrimSpace(raw)
	if cmd == "" {
		return -1, ErrTerminalEmptyCmd
	}
	if len(cmd) > maxCmdLen {
		return -1, ErrTerminalTooLong
	}

	s.mu.Lock()
	cwd := s.Cwd
	if cwd == "" {
		cwd = s.Root
	}
	// Perintah "cd <dir>" (tanpa embel-embel): pindahkan posisi sesi.
	if target, ok := parseCd(cmd); ok {
		if target == "-" {
			// "cd -": kembali ke folder sebelumnya.
			if s.Prev == "" {
				s.mu.Unlock()
				fmt.Fprintln(w, "bash: cd: OLDPWD not set")
				return 1, nil
			}
			s.Prev, s.Cwd = cwd, s.Prev
			s.mu.Unlock()
			fmt.Fprintln(w, s.Cwd)
			return 0, nil
		}
		next := target
		if !filepath.IsAbs(next) {
			next = filepath.Join(cwd, next)
		}
		next = filepath.Clean(next)
		info, err := os.Stat(next)
		if err != nil || !info.IsDir() {
			s.mu.Unlock()
			fmt.Fprintf(w, "bash: cd: %s: No such file or directory\n", target)
			return 1, nil
		}
		s.Prev = cwd
		s.Cwd = next
		s.mu.Unlock()
		return 0, nil
	}
	s.mu.Unlock()

	command := exec.CommandContext(ctx, "bash", "-c", cmd)
	command.Env = append(os.Environ(), "PWD="+cwd, "HOME="+cwd)
	command.Dir = cwd
	return streamCmd(ctx, command, w)
}

// parseCd mengenali perintah cd murni: "cd", "cd <dir>", "cd -", "cd ~".
// Mengembalikan false bila ada operator/rangkaian lain (&&, ;, |, ...)
// supaya tetap dijalankan sebagai perintah biasa.
func parseCd(cmd string) (string, bool) {
	rest, ok := strings.CutPrefix(cmd, "cd")
	if !ok {
		return "", false
	}
	if rest == "" {
		return "~", true
	}
	if rest[0] != ' ' && rest[0] != '	' {
		// Bukan "cd", melainkan kata lain (cdr, cdx, ...).
		return "", false
	}
	target := strings.TrimSpace(rest)
	if target == "" {
		return "~", true
	}
	// Tolak bila ada operator shell — biar bash yang menanganinya.
	if strings.ContainsAny(target, ";|&<>`$(){}!*?#~'\"\\\r\n") {
		return "", false
	}
	// Tilde expansion sederhana (bash -c non-interaktif tidak selalu menanganinya
	// konsisten untuk "cd ~" di semua setup).
	if target == "~" || strings.HasPrefix(target, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			target = filepath.Join(home, strings.TrimPrefix(target, "~"))
		}
	}
	return target, true
}

// CwdSaatIni untuk menampilkan posisi sesi (dipakai handler "ready"/prompt).
func (s *Session) CwdSaatIni() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Cwd == "" {
		return s.Root
	}
	return s.Cwd
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
