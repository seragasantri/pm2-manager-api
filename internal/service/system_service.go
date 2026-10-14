package service

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SystemService mengumpulkan informasi server ala "Server Information" cPanel.
type SystemService struct {
	mu        sync.Mutex
	versions  map[string]string
	versionAt time.Time
}

func NewSystemService() *SystemService { return &SystemService{} }

// ServiceStatus status satu unit systemd.
type ServiceStatus struct {
	Name   string `json:"name"`
	Unit   string `json:"unit"`
	Active string `json:"active"` // active | inactive | failed | unknown
}

// NetIface ringkas satu interface jaringan.
type NetIface struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
	RX    uint64   `json:"rx"`
	TX    uint64   `json:"tx"`
}

// SystemOverview ringkasan host.
type SystemOverview struct {
	Hostname   string            `json:"hostname"`
	OS         string            `json:"os"`
	Kernel     string            `json:"kernel"`
	Arch       string            `json:"arch"`
	CPUModel   string            `json:"cpuModel"`
	CPUCores   int               `json:"cpuCores"`
	Timezone   string            `json:"timezone"`
	ServerTime string            `json:"serverTime"`
	Services   []ServiceStatus   `json:"services"`
	Versions   map[string]string `json:"versions"`
	Network    []NetIface        `json:"network"`
	Disks      []DiskUsage       `json:"disks"`
}

// DiskUsage untuk satu mount point.
type DiskUsage struct {
	Mount string `json:"mount"`
	FS    string `json:"fs"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

func readOSRelease() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return runtime.GOOS
}

func readCPUInfo() (string, int) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", runtime.NumCPU()
	}
	defer f.Close()
	model := ""
	cores := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "model name") && model == "" {
			if i := strings.Index(line, ":"); i >= 0 {
				model = strings.TrimSpace(line[i+1:])
			}
		}
		if strings.HasPrefix(line, "processor") {
			cores++
		}
	}
	if cores == 0 {
		cores = runtime.NumCPU()
	}
	return model, cores
}

// ReadNetDev membaca total byte rx/tx per interface dari /proc/net/dev.
func ReadNetDev() map[string][2]uint64 {
	out := map[string][2]uint64{}
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n")[2:] {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		f := strings.Fields(line[i+1:])
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out[name] = [2]uint64{rx, tx}
	}
	return out
}

func isVirtualIface(name string) bool {
	for _, p := range []string{"lo", "veth", "docker", "br-", "virbr", "lxc", "lxd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func systemctlActive(ctx context.Context, units []string) map[string]string {
	res := map[string]string{}
	if len(units) == 0 {
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	bin := findBin("systemctl")
	if bin == "" {
		return res
	}
	out, _ := exec.CommandContext(ctx, bin, append([]string{"is-active"}, units...)...).Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, u := range units {
		st := "unknown"
		if i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			st = strings.TrimSpace(lines[i])
		}
		res[u] = st
	}
	return res
}

func phpFpmUnits() []string {
	var units []string
	matches, _ := filepath.Glob("/lib/systemd/system/php*-fpm.service")
	more, _ := filepath.Glob("/etc/systemd/system/multi-user.target.wants/php*-fpm.service")
	seen := map[string]bool{}
	for _, m := range append(matches, more...) {
		u := filepath.Base(m)
		if !seen[u] {
			seen[u] = true
			units = append(units, u)
		}
	}
	sort.Strings(units)
	return units
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

var reVersion = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// findBin mencari executable di PATH lalu di direktori sbin (service systemd
// sering berjalan dengan PATH minimal tanpa /usr/sbin, tempat nginx berada).
func findBin(bin string) string {
	if filepath.IsAbs(bin) {
		return bin
	}
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	for _, dir := range []string{"/usr/sbin", "/usr/local/sbin", "/sbin", "/usr/local/bin", "/usr/bin"} {
		p := filepath.Join(dir, bin)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func cmdVersion(ctx context.Context, bin string, args ...string) string {
	path := findBin(bin)
	if path == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	if v := reVersion.FindString(firstLine(string(out))); v != "" {
		return v
	}
	return firstLine(string(out))
}

func (s *SystemService) versionsCached(ctx context.Context, pm2Bin string) map[string]string {
	s.mu.Lock()
	if s.versions != nil && time.Since(s.versionAt) < 10*time.Minute {
		v := s.versions
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()

	type job struct {
		key, bin string
		args     []string
	}
	jobs := []job{
		{"nginx", "nginx", []string{"-v"}},
		{"php", "php", []string{"-v"}},
		{"node", "node", []string{"-v"}},
		{"npm", "npm", []string{"-v"}},
		{"pm2", pm2Bin, []string{"-v"}},
		{"docker", "docker", []string{"--version"}},
		{"mysql", "mysql", []string{"--version"}},
		{"git", "git", []string{"--version"}},
		{"composer", "composer", []string{"--version", "--no-ansi"}},
	}
	res := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, j := range jobs {
		if j.bin == "" {
			continue
		}
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			if v := cmdVersion(ctx, j.bin, j.args...); v != "" {
				mu.Lock()
				res[j.key] = v
				mu.Unlock()
			}
		}(j)
	}
	wg.Wait()

	s.mu.Lock()
	s.versions = res
	s.versionAt = time.Now()
	s.mu.Unlock()
	return res
}

func readMounts() []DiskUsage {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	var out []DiskUsage
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		mount := f[1]
		if seen[f[0]] || strings.HasPrefix(mount, "/snap") || strings.HasPrefix(mount, "/var/lib/docker") {
			continue
		}
		seen[f[0]] = true
		total, used, ok := statfs(mount)
		if !ok || total == 0 {
			continue
		}
		out = append(out, DiskUsage{Mount: mount, FS: f[2], Total: total, Used: used})
	}
	return out
}

// Overview mengumpulkan semua info host.
func (s *SystemService) Overview(ctx context.Context, pm2Bin string) *SystemOverview {
	o := &SystemOverview{OS: readOSRelease(), Arch: runtime.GOARCH}
	o.Hostname, _ = os.Hostname()
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		o.Kernel = strings.TrimSpace(string(b))
	}
	o.CPUModel, o.CPUCores = readCPUInfo()
	now := time.Now()
	o.ServerTime = now.Format(time.RFC3339)
	o.Timezone, _ = now.Zone()
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		o.Timezone = strings.TrimSpace(string(b))
	}

	units := []struct{ name, unit string }{
		{"Nginx", "nginx.service"},
		{"MySQL", "mysql.service"},
		{"MariaDB", "mariadb.service"},
		{"Docker", "docker.service"},
		{"PM2", "pm2-" + os.Getenv("USER") + ".service"},
		{"Cron", "cron.service"},
		{"SSH", "ssh.service"},
		{"Firewall", "ufw.service"},
	}
	for _, u := range phpFpmUnits() {
		label := strings.TrimSuffix(u, ".service")
		label = strings.Replace(strings.Replace(label, "php", "PHP ", 1), "-fpm", " FPM", 1)
		units = append(units, struct{ name, unit string }{label, u})
	}
	names := make([]string, 0, len(units))
	for _, u := range units {
		names = append(names, u.unit)
	}
	states := systemctlActive(ctx, names)
	for _, u := range units {
		st := states[u.unit]
		if st == "" {
			st = "unknown"
		}
		// Lewati unit yang memang tidak terpasang (mis. MariaDB di server MySQL).
		if st == "inactive" && !unitInstalled(u.unit) {
			continue
		}
		if st == "unknown" && !unitInstalled(u.unit) {
			continue
		}
		o.Services = append(o.Services, ServiceStatus{Name: u.name, Unit: u.unit, Active: st})
	}

	o.Versions = s.versionsCached(ctx, pm2Bin)

	dev := ReadNetDev()
	ifs, _ := net.Interfaces()
	for _, it := range ifs {
		if isVirtualIface(it.Name) {
			continue
		}
		ni := NetIface{Name: it.Name}
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			ni.Addrs = append(ni.Addrs, a.String())
		}
		if v, ok := dev[it.Name]; ok {
			ni.RX, ni.TX = v[0], v[1]
		}
		o.Network = append(o.Network, ni)
	}
	o.Disks = readMounts()
	return o
}

func unitInstalled(unit string) bool {
	for _, dir := range []string{"/lib/systemd/system", "/etc/systemd/system", "/usr/lib/systemd/system"} {
		if _, err := os.Stat(filepath.Join(dir, unit)); err == nil {
			return true
		}
	}
	return false
}
