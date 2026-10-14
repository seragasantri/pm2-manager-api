package service

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Terminal root (PTY) — shell interaktif sungguhan untuk superadmin, setara
// login SSH: vim, htop, sudo, tab-completion, warna, semuanya jalan.
//
// Output tiap sesi disimpan di ring buffer berbasis offset byte, sehingga:
//   - klien bisa menyambung ulang (reload halaman, koneksi putus) tanpa
//     kehilangan layar — cukup minta data mulai offset terakhir;
//   - transport bebas: WebSocket (cepat) atau SSE + POST (lolos Cloudflare /
//     proxy yang membuang header Upgrade).

var (
	ErrPtyNotFound    = errors.New("sesi terminal tidak ditemukan")
	ErrPtyLimit       = errors.New("jumlah sesi terminal sudah maksimum, tutup sesi lain dulu")
	ErrPtyClosed      = errors.New("sesi terminal sudah berakhir")
	ErrPtyInvalidSize = errors.New("ukuran terminal tidak valid")
)

const (
	ptyBufferMax   = 1 << 20 // simpan 1 MiB output terakhir per sesi
	ptyMaxSessions = 12
	ptyIdleTTL     = 30 * time.Minute // sesi tanpa penonton dimatikan setelah ini
	ptyExitedTTL   = 5 * time.Minute  // sesi yang sudah exit dibersihkan setelah ini
)

// PtyManager memegang semua sesi PTY yang hidup.
type PtyManager struct {
	mu       sync.Mutex
	sessions map[string]*PtySession
	shell    string
	home     string
	user     string
	hostname string
}

// PtySession adalah satu shell interaktif.
type PtySession struct {
	ID        string
	Title     string
	Shell     string
	StartDir  string
	CreatedBy string
	CreatedAt time.Time

	cmd *exec.Cmd
	tty *os.File

	mu         sync.Mutex
	buf        []byte
	base       int64         // offset absolut dari buf[0]
	changed    chan struct{} // ditutup setiap ada output baru / sesi berakhir
	viewers    int
	lastSeen   time.Time
	cols, rows uint16
	exited     bool
	exitCode   int
	exitedAt   time.Time
	done       chan struct{}
	killOnce   sync.Once
}

// PtyInfo adalah ringkasan sesi untuk API.
type PtyInfo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Shell     string    `json:"shell"`
	StartDir  string    `json:"startDir"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	Viewers   int       `json:"viewers"`
	Cols      uint16    `json:"cols"`
	Rows      uint16    `json:"rows"`
	Exited    bool      `json:"exited"`
	ExitCode  int       `json:"exitCode"`
	Offset    int64     `json:"offset"`
	Pid       int       `json:"pid"`
}

func NewPtyManager() *PtyManager {
	m := &PtyManager{sessions: map[string]*PtySession{}}
	m.user, m.home, m.shell = currentAccount()
	m.hostname, _ = os.Hostname()
	go m.reaper()
	return m
}

// currentAccount membaca akun proses saat ini dari /etc/passwd (tanpa cgo).
func currentAccount() (username, home, shell string) {
	uid := strconv.Itoa(os.Getuid())
	if f, err := os.Open("/etc/passwd"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			parts := strings.Split(sc.Text(), ":")
			if len(parts) >= 7 && parts[2] == uid {
				username, home, shell = parts[0], parts[5], parts[6]
				break
			}
		}
	}
	if username == "" {
		username = os.Getenv("USER")
	}
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/"
	}
	if shell == "" || strings.HasSuffix(shell, "nologin") || strings.HasSuffix(shell, "false") {
		shell = os.Getenv("SHELL")
	}
	for _, cand := range []string{shell, "/bin/bash", "/usr/bin/bash", "/bin/zsh", "/bin/sh"} {
		if cand == "" {
			continue
		}
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return username, home, cand
		}
	}
	return username, home, "/bin/sh"
}

// cleanEnv sengaja TIDAK mewarisi environment service (berisi DB_PASSWORD,
// JWT_SECRET, dsb). Shell login akan memuat profil user sendiri.
func (m *PtyManager) cleanEnv() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "C.UTF-8"
	}
	return []string{
		"HOME=" + m.home,
		"USER=" + m.user,
		"LOGNAME=" + m.user,
		"SHELL=" + m.shell,
		"PATH=" + path,
		"LANG=" + lang,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"PANELPM_TERMINAL=1",
	}
}

func newPtyID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create membuka shell login baru di PTY.
func (m *PtyManager) Create(cols, rows uint16, startDir, createdBy string) (*PtySession, error) {
	if cols == 0 || rows == 0 {
		cols, rows = 120, 32
	}
	if cols > 1000 || rows > 500 {
		return nil, ErrPtyInvalidSize
	}

	m.mu.Lock()
	alive := 0
	for _, s := range m.sessions {
		if !s.isExited() {
			alive++
		}
	}
	m.mu.Unlock()
	if alive >= ptyMaxSessions {
		return nil, ErrPtyLimit
	}

	dir := m.home
	if startDir != "" {
		if st, err := os.Stat(startDir); err == nil && st.IsDir() && filepath.IsAbs(startDir) {
			dir = filepath.Clean(startDir)
		}
	}

	cmd := exec.Command(m.shell, "-l")
	cmd.Dir = dir
	cmd.Env = m.cleanEnv()
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}

	s := &PtySession{
		ID:        newPtyID(),
		Title:     m.user + "@" + m.hostname,
		Shell:     m.shell,
		StartDir:  dir,
		CreatedBy: createdBy,
		CreatedAt: time.Now(),
		cmd:       cmd,
		tty:       tty,
		changed:   make(chan struct{}),
		lastSeen:  time.Now(),
		cols:      cols,
		rows:      rows,
		done:      make(chan struct{}),
	}
	go s.pump()
	go s.wait()

	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return s, nil
}

// Get mengambil sesi berdasar ID.
func (m *PtyManager) Get(id string) (*PtySession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrPtyNotFound
	}
	return s, nil
}

// List mengembalikan semua sesi, terbaru dulu.
func (m *PtyManager) List() []PtyInfo {
	m.mu.Lock()
	out := make([]PtyInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Info())
	}
	m.mu.Unlock()
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CreatedAt.After(out[j-1].CreatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Close mematikan sesi dan menghapusnya dari daftar.
func (m *PtyManager) Close(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if !ok {
		return ErrPtyNotFound
	}
	s.kill()
	return nil
}

// Shutdown mematikan semua sesi (dipanggil saat server berhenti).
func (m *PtyManager) Shutdown() {
	m.mu.Lock()
	all := m.sessions
	m.sessions = map[string]*PtySession{}
	m.mu.Unlock()
	for _, s := range all {
		s.kill()
	}
}

func (m *PtyManager) reaper() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		var victims []*PtySession
		m.mu.Lock()
		for id, s := range m.sessions {
			s.mu.Lock()
			idle := s.viewers == 0 && now.Sub(s.lastSeen) > ptyIdleTTL
			stale := s.exited && s.viewers == 0 && now.Sub(s.exitedAt) > ptyExitedTTL
			s.mu.Unlock()
			if idle || stale {
				delete(m.sessions, id)
				victims = append(victims, s)
			}
		}
		m.mu.Unlock()
		for _, s := range victims {
			s.kill()
		}
	}
}

// ─── Sesi ──────────────────────────────────────────────────────────────────

func (s *PtySession) pump() {
	chunk := make([]byte, 32*1024)
	for {
		n, err := s.tty.Read(chunk)
		if n > 0 {
			s.mu.Lock()
			s.buf = append(s.buf, chunk[:n]...)
			if over := len(s.buf) - ptyBufferMax; over > 0 {
				s.buf = append([]byte(nil), s.buf[over:]...)
				s.base += int64(over)
			}
			close(s.changed)
			s.changed = make(chan struct{})
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *PtySession) wait() {
	err := s.cmd.Wait()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	// Beri pump kesempatan menguras sisa output sebelum menandai selesai.
	time.Sleep(60 * time.Millisecond)
	s.mu.Lock()
	s.exited = true
	s.exitCode = code
	s.exitedAt = time.Now()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
	close(s.done)
	_ = s.tty.Close()
}

func (s *PtySession) kill() {
	s.killOnce.Do(func() {
		if s.cmd.Process == nil {
			return
		}
		pgid := s.cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGHUP)
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	})
}

func (s *PtySession) isExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// Info membuat ringkasan sesi.
func (s *PtySession) Info() PtyInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	pid := 0
	if s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}
	return PtyInfo{
		ID: s.ID, Title: s.Title, Shell: s.Shell, StartDir: s.StartDir,
		CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt, Viewers: s.viewers,
		Cols: s.cols, Rows: s.rows, Exited: s.exited, ExitCode: s.exitCode,
		Offset: s.base + int64(len(s.buf)), Pid: pid,
	}
}

// Write mengirim input (ketikan) ke shell.
func (s *PtySession) Write(p []byte) error {
	if s.isExited() {
		return ErrPtyClosed
	}
	s.touch()
	_, err := s.tty.Write(p)
	return err
}

// Resize mengubah ukuran jendela PTY (SIGWINCH ke proses di dalamnya).
func (s *PtySession) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 || cols > 1000 || rows > 500 {
		return ErrPtyInvalidSize
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	exited := s.exited
	s.mu.Unlock()
	if exited {
		return nil
	}
	return pty.Setsize(s.tty, &pty.Winsize{Cols: cols, Rows: rows})
}

func (s *PtySession) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

// Attach mencatat satu penonton aktif; panggil fungsi yang dikembalikan saat lepas.
func (s *PtySession) Attach() (detach func()) {
	s.mu.Lock()
	s.viewers++
	s.lastSeen = time.Now()
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.viewers--
			s.lastSeen = time.Now()
			s.mu.Unlock()
		})
	}
}

// PtyChunk adalah potongan output mulai Offset.
type PtyChunk struct {
	Offset    int64
	Data      []byte
	Truncated bool // sebagian output lama sudah terbuang dari buffer
}

// ReadFrom mengambil output mulai offset. Bila belum ada data baru,
// kembalikan channel yang akan ditutup saat ada perubahan.
func (s *PtySession) ReadFrom(off int64, max int) (PtyChunk, <-chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	end := s.base + int64(len(s.buf))
	ch := PtyChunk{Offset: off}
	if off < 0 || off > end {
		off = end
		ch.Offset = end
	}
	if off < s.base {
		ch.Truncated = true
		off = s.base
		ch.Offset = off
	}
	if off < end {
		data := s.buf[off-s.base:]
		if max > 0 && len(data) > max {
			data = data[:max]
		}
		ch.Data = append([]byte(nil), data...)
	}
	return ch, s.changed, s.exited && off+int64(len(ch.Data)) >= end
}

// ExitCode mengembalikan kode keluar (valid bila sesi sudah exit).
func (s *PtySession) ExitCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode
}

// Stream memanggil emit untuk setiap potongan output mulai offset sampai ctx
// selesai atau sesi berakhir. heartbeat dipanggil bila tidak ada output selama
// interval (untuk menjaga koneksi proxy tetap hidup).
func (s *PtySession) Stream(ctx context.Context, off int64, heartbeat time.Duration,
	emit func(PtyChunk) error, beat func() error) (exited bool, err error) {
	detach := s.Attach()
	defer detach()

	timer := time.NewTimer(heartbeat)
	defer timer.Stop()
	for {
		chunk, changed, finished := s.ReadFrom(off, 64*1024)
		if len(chunk.Data) > 0 || chunk.Truncated {
			if err := emit(chunk); err != nil {
				return false, err
			}
			off = chunk.Offset + int64(len(chunk.Data))
			continue
		}
		off = chunk.Offset
		if finished {
			return true, nil
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(heartbeat)
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-changed:
		case <-timer.C:
			if beat != nil {
				if err := beat(); err != nil {
					return false, err
				}
			}
			s.touch()
		}
	}
}
