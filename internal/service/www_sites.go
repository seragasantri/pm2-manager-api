package service

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Website adalah satu aplikasi yang terdeteksi di bawah root /var/www.
type Website struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`    // relatif terhadap root, mis. "/simobe-lpm"
	AbsPath   string   `json:"absPath"` // path absolut di server
	Framework string   `json:"framework"`
	Version   string   `json:"version,omitempty"`
	Language  string   `json:"language"`
	Domains   []string `json:"domains"`
	SSL       bool     `json:"ssl"`
	PHP       string   `json:"php,omitempty"` // socket php-fpm dari konfigurasi nginx
	Runtime   string   `json:"runtime"`       // nginx | pm2 | docker | none
	Proxy     []string `json:"proxy,omitempty"`
	DocRoot   string   `json:"docRoot,omitempty"`
	Git       *GitMeta `json:"git,omitempty"`
	HasEnv    bool     `json:"hasEnv"`
	ModTime   int64    `json:"mtime"`
	Markers   []string `json:"markers"`
}

// GitMeta ringkas dari .git tanpa menjalankan perintah git.
type GitMeta struct {
	Branch string `json:"branch"`
	Commit string `json:"commit,omitempty"`
	Remote string `json:"remote,omitempty"`
}

// NginxServer adalah ringkasan satu blok server{} nginx.
type NginxServer struct {
	File       string   `json:"file"`
	Names      []string `json:"names"`
	Roots      []string `json:"roots"`
	ProxyPass  []string `json:"proxyPass"`
	SSL        bool     `json:"ssl"`
	PHPSockets []string `json:"phpSockets"`
}

var (
	reNginxDirective = regexp.MustCompile(`^\s*(server_name|root|alias|proxy_pass|listen|fastcgi_pass)\s+([^;]+);`)
	reNginxSplit     = regexp.MustCompile(`[{};]`)
)

// ParseNginx membaca semua file di sites-enabled (best-effort, tanpa include).
func (s *WWWService) ParseNginx() []NginxServer {
	files, _ := filepath.Glob(filepath.Join(s.nginxDir, "*"))
	var out []NginxServer
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		var cur *NginxServer
		depth := 0
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if i := strings.Index(line, "#"); i >= 0 {
				line = line[:i]
			}
			trim := strings.TrimSpace(line)
			if depth == 0 && strings.HasPrefix(trim, "server") && strings.Contains(trim, "{") {
				cur = &NginxServer{File: filepath.Base(f)}
			}
			if cur != nil {
				// Satu baris bisa memuat beberapa pernyataan, mis.
				// "location ~ \.php$ { fastcgi_pass ...; }".
				for _, stmt := range reNginxSplit.Split(line, -1) {
					m := reNginxDirective.FindStringSubmatch(stmt + ";")
					if m == nil {
						continue
					}
					val := strings.TrimSpace(m[2])
					switch m[1] {
					case "server_name":
						for _, n := range strings.Fields(val) {
							if n != "_" && n != "localhost" {
								cur.Names = append(cur.Names, n)
							}
						}
					case "root", "alias":
						r := filepath.Clean(strings.Trim(val, `"'`))
						if real, err := filepath.EvalSymlinks(r); err == nil {
							r = real
						}
						cur.Roots = append(cur.Roots, r)
					case "proxy_pass":
						cur.ProxyPass = append(cur.ProxyPass, val)
					case "listen":
						if strings.Contains(val, "ssl") || strings.HasPrefix(val, "443") {
							cur.SSL = true
						}
					case "fastcgi_pass":
						cur.PHPSockets = append(cur.PHPSockets, val)
					}
				}
			}
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth <= 0 && cur != nil && strings.Contains(line, "}") {
				out = append(out, *cur)
				cur = nil
				depth = 0
			}
		}
		fh.Close()
	}
	return out
}

type siteMarker struct {
	file      string
	framework string
	language  string
}

// Urutan penting: penanda paling spesifik dulu.
var siteMarkers = []siteMarker{
	{"artisan", "Laravel", "PHP"},
	{"spark", "CodeIgniter 4", "PHP"},
	{"system/CodeIgniter.php", "CodeIgniter 3", "PHP"},
	{"application/config/config.php", "CodeIgniter 3", "PHP"},
	{"wp-config.php", "WordPress", "PHP"},
	{"wp-load.php", "WordPress", "PHP"},
	{"bin/console", "Symfony", "PHP"},
	{"yii", "Yii", "PHP"},
	{"next.config.js", "Next.js", "Node.js"},
	{"next.config.mjs", "Next.js", "Node.js"},
	{"next.config.ts", "Next.js", "Node.js"},
	{"nuxt.config.ts", "Nuxt", "Node.js"},
	{"nuxt.config.js", "Nuxt", "Node.js"},
	{"vite.config.js", "Vite", "Node.js"},
	{"vite.config.ts", "Vite", "Node.js"},
	{"go.mod", "Go", "Go"},
	{"manage.py", "Django", "Python"},
	{"requirements.txt", "Python", "Python"},
	{"composer.json", "PHP (Composer)", "PHP"},
	{"package.json", "Node.js", "Node.js"},
	{"index.php", "PHP Native", "PHP"},
	{"public/index.php", "PHP Native", "PHP"},
	{"index.html", "Static HTML", "HTML"},
	{"public/index.html", "Static HTML", "HTML"},
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func detectFramework(dir string) (framework, language string, markers []string) {
	for _, m := range siteMarkers {
		if fileExists(filepath.Join(dir, m.file)) {
			markers = append(markers, m.file)
			if framework == "" {
				framework, language = m.framework, m.language
			}
		}
	}
	return
}

func composerVersion(dir, pkg string) string {
	b, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return ""
	}
	var cj struct {
		Require map[string]string `json:"require"`
	}
	if json.Unmarshal(b, &cj) != nil {
		return ""
	}
	return cj.Require[pkg]
}

func packageVersion(dir, pkg string) string {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pj struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(b, &pj) != nil {
		return ""
	}
	if v := pj.Dependencies[pkg]; v != "" {
		return v
	}
	return pj.DevDependencies[pkg]
}

func readGitMeta(dir string) *GitMeta {
	gitDir := filepath.Join(dir, ".git")
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return nil
	}
	g := &GitMeta{}
	h := strings.TrimSpace(string(head))
	if strings.HasPrefix(h, "ref: ") {
		ref := strings.TrimPrefix(h, "ref: ")
		g.Branch = strings.TrimPrefix(ref, "refs/heads/")
		if b, err := os.ReadFile(filepath.Join(gitDir, ref)); err == nil {
			g.Commit = strings.TrimSpace(string(b))
		} else if pr, err := os.ReadFile(filepath.Join(gitDir, "packed-refs")); err == nil {
			for _, line := range strings.Split(string(pr), "\n") {
				if strings.HasSuffix(line, " "+ref) {
					g.Commit = strings.Fields(line)[0]
				}
			}
		}
	} else {
		g.Branch = "(detached)"
		g.Commit = h
	}
	if len(g.Commit) > 10 {
		g.Commit = g.Commit[:10]
	}
	if cfg, err := os.ReadFile(filepath.Join(gitDir, "config")); err == nil {
		inOrigin := false
		for _, line := range strings.Split(string(cfg), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "[") {
				inOrigin = t == `[remote "origin"]`
			}
			if inOrigin && strings.HasPrefix(t, "url") {
				if i := strings.Index(t, "="); i >= 0 {
					g.Remote = redactURL(strings.TrimSpace(t[i+1:]))
				}
			}
		}
	}
	return g
}

// redactURL membuang kredensial (user:token@) dari URL remote git.
func redactURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 && at < strings.Index(rest+"/", "/") {
			return u[:i+3] + rest[at+1:]
		}
	}
	return u
}

// Sites mendeteksi aplikasi di root. Folder yang punya penanda framework
// dianggap satu aplikasi; folder tanpa penanda ditelusuri sampai kedalaman 3.
func (s *WWWService) Sites(ctx context.Context, pm2Cwds, dockerDirs []string) []Website {
	servers := s.ParseNginx()
	var sites []Website

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if ctx.Err() != nil {
			return
		}
		fw, lang, markers := detectFramework(dir)
		isGit := pathExists(filepath.Join(dir, ".git"))
		if fw != "" || (isGit && dir != s.root) {
			if fw == "" {
				fw, lang = "Unknown", "-"
			}
			sites = append(sites, s.buildSite(dir, fw, lang, markers, servers, pm2Cwds, dockerDirs))
			return
		}
		if depth >= 3 {
			return
		}
		items, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, it := range items {
			name := it.Name()
			if strings.HasPrefix(name, ".") || wwwSkipDirs[name] {
				continue
			}
			p := filepath.Join(dir, name)
			if it.Type()&os.ModeSymlink != 0 {
				// Symlink ke luar root (mis. phpmyadmin) tetap ditampilkan
				// sebagai situs, tapi tidak ditelusuri lebih dalam.
				if real, err := filepath.EvalSymlinks(p); err == nil {
					if st, err := os.Stat(real); err == nil && st.IsDir() {
						fw, lang, markers := detectFramework(real)
						if fw == "" {
							fw, lang = "Symlink", "-"
						}
						site := s.buildSite(p, fw, lang, markers, servers, pm2Cwds, dockerDirs)
						site.Markers = append(site.Markers, "symlink → "+real)
						sites = append(sites, site)
					}
				}
				continue
			}
			if it.IsDir() {
				walk(p, depth+1)
			}
		}
	}
	walk(s.root, 0)

	sort.Slice(sites, func(i, j int) bool { return sites[i].Path < sites[j].Path })
	return sites
}

func (s *WWWService) buildSite(dir, fw, lang string, markers []string, servers []NginxServer, pm2Cwds, dockerDirs []string) Website {
	site := Website{
		Name:      filepath.Base(dir),
		Path:      s.toRel(dir),
		AbsPath:   dir,
		Framework: fw,
		Language:  lang,
		Markers:   markers,
		Domains:   []string{},
		Runtime:   "none",
		HasEnv:    fileExists(filepath.Join(dir, ".env")),
		Git:       readGitMeta(dir),
	}
	if st, err := os.Stat(dir); err == nil {
		site.ModTime = st.ModTime().Unix()
	}
	switch fw {
	case "Laravel":
		site.Version = composerVersion(dir, "laravel/framework")
	case "CodeIgniter 4":
		site.Version = composerVersion(dir, "codeigniter4/framework")
	case "Symfony":
		site.Version = composerVersion(dir, "symfony/framework-bundle")
	case "Next.js":
		site.Version = packageVersion(dir, "next")
	case "Nuxt":
		site.Version = packageVersion(dir, "nuxt")
	case "Vite":
		site.Version = packageVersion(dir, "vite")
	}

	inside := func(p string) bool {
		return p == dir || strings.HasPrefix(p, dir+string(os.PathSeparator))
	}
	seen := map[string]bool{}
	for _, srv := range servers {
		match := false
		for _, r := range srv.Roots {
			if inside(r) {
				match = true
				if site.DocRoot == "" && !strings.Contains(r, "/storage") {
					site.DocRoot = r
				}
			}
		}
		if !match {
			continue
		}
		site.Runtime = "nginx"
		if srv.SSL {
			site.SSL = true
		}
		for _, n := range srv.Names {
			if !seen[n] {
				seen[n] = true
				site.Domains = append(site.Domains, n)
			}
		}
		if len(srv.PHPSockets) > 0 && site.PHP == "" {
			site.PHP = phpLabel(srv.PHPSockets[0])
		}
		site.Proxy = append(site.Proxy, srv.ProxyPass...)
	}
	for _, c := range pm2Cwds {
		if inside(c) {
			site.Runtime = "pm2"
			break
		}
	}
	for _, d := range dockerDirs {
		if d != "" && (inside(d) || strings.HasPrefix(dir, d+string(os.PathSeparator))) {
			site.Runtime = "docker"
			break
		}
	}
	return site
}

var rePHPVer = regexp.MustCompile(`php(\d+\.\d+)`)

func phpLabel(sock string) string {
	if m := rePHPVer.FindStringSubmatch(sock); m != nil {
		return "PHP " + m[1] + " FPM"
	}
	if strings.Contains(sock, "php") {
		return "PHP FPM"
	}
	return sock
}
