package service

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// WWWService adalah File Manager untuk aplikasi web di /var/www (Laravel,
// CodeIgniter, PHP native, statis, dll — yang dilayani Nginx tanpa PM2/Docker).
//
// Semua path dari/ke klien bersifat RELATIF terhadap root (diawali "/"), mis.
// "/simobe-lpm/app". Tidak ada operasi yang boleh keluar dari root — termasuk
// lewat symlink (target symlink diselesaikan dulu sebelum dicek).
type WWWService struct {
	root     string // path absolut root yang sudah di-resolve (tanpa symlink)
	nginxDir string

	idMu   sync.Mutex
	users  map[uint32]string
	groups map[uint32]string
	idLoad time.Time
}

var (
	ErrWWWNotFound   = errors.New("file atau folder tidak ditemukan")
	ErrWWWExists     = errors.New("nama sudah dipakai di folder tujuan")
	ErrWWWIsDir      = errors.New("path adalah folder")
	ErrWWWNotDir     = errors.New("path bukan folder")
	ErrWWWBinary     = errors.New("file biner tidak bisa dibuka di editor")
	ErrWWWTooLarge   = errors.New("file terlalu besar untuk dibuka di editor (maks 5 MB)")
	ErrWWWRoot       = errors.New("root tidak boleh diubah atau dihapus")
	ErrWWWConflict   = errors.New("file sudah berubah di server sejak dibuka")
	ErrWWWBadName    = errors.New("nama file tidak valid")
	ErrWWWBadMode    = errors.New("permission harus oktal 3-4 digit, mis. 755")
	ErrWWWUnsupport  = errors.New("format arsip tidak didukung (zip, tar, tar.gz, tgz)")
	ErrWWWIntoItself = errors.New("tidak bisa menyalin/memindah folder ke dalam dirinya sendiri")
)

const wwwEditorMax = 5 << 20

func NewWWWService(root, nginxDir string) *WWWService {
	if root == "" {
		root = "/var/www"
	}
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if nginxDir == "" {
		nginxDir = "/etc/nginx/sites-enabled"
	}
	return &WWWService{root: filepath.Clean(root), nginxDir: nginxDir}
}

// Root mengembalikan path absolut root.
func (s *WWWService) Root() string { return s.root }

func (s *WWWService) within(abs string) bool {
	return abs == s.root || strings.HasPrefix(abs, s.root+string(os.PathSeparator))
}

// clean menormalkan path relatif dari klien jadi bentuk "/a/b".
func cleanRel(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	return filepath.ToSlash(filepath.Clean("/" + rel))
}

// toRel mengubah path absolut (di dalam root) jadi path relatif untuk klien.
func (s *WWWService) toRel(abs string) string {
	r, err := filepath.Rel(s.root, abs)
	if err != nil || r == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(r)
}

// evalExisting me-resolve symlink pada bagian path yang sudah ada,
// lalu menyambung kembali sisa path yang belum ada.
func evalExisting(p string) (string, error) {
	cur := p
	var tail []string
	for {
		if _, err := os.Lstat(cur); err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return real, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// resolve: path target yang isinya akan dibaca/ditulis/dijelajahi.
// Symlink diikuti dan hasil akhirnya wajib tetap di dalam root.
func (s *WWWService) resolve(rel string) (string, error) {
	abs := filepath.Join(s.root, cleanRel(rel))
	real, err := evalExisting(abs)
	if err != nil {
		return "", ErrFileAccessDenied
	}
	if !s.within(real) {
		return "", ErrFileAccessDenied
	}
	return real, nil
}

// resolveEntry: path entri itu sendiri (untuk hapus/rename symlink tanpa
// menyentuh targetnya). Hanya folder induknya yang di-resolve.
func (s *WWWService) resolveEntry(rel string) (string, error) {
	c := cleanRel(rel)
	if c == "/" {
		return s.root, nil
	}
	parent, err := s.resolve(filepath.Dir(c))
	if err != nil {
		return "", err
	}
	name := filepath.Base(c)
	if name == "." || name == ".." || name == "/" {
		return "", ErrWWWBadName
	}
	return filepath.Join(parent, name), nil
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00")
}

// ─── Pemilik file (uid/gid → nama) ─────────────────────────────────────────

func parseIDFile(path string) map[uint32]string {
	out := map[uint32]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ":")
		if len(parts) < 3 {
			continue
		}
		if id, err := strconv.ParseUint(parts[2], 10, 32); err == nil {
			out[uint32(id)] = parts[0]
		}
	}
	return out
}

func (s *WWWService) idNames(uid, gid uint32) (string, string) {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	if s.users == nil || time.Since(s.idLoad) > 5*time.Minute {
		s.users = parseIDFile("/etc/passwd")
		s.groups = parseIDFile("/etc/group")
		s.idLoad = time.Now()
	}
	u, ok := s.users[uid]
	if !ok {
		u = strconv.Itoa(int(uid))
	}
	g, ok := s.groups[gid]
	if !ok {
		g = strconv.Itoa(int(gid))
	}
	return u, g
}

// ─── Listing ───────────────────────────────────────────────────────────────

// WWWEntry adalah satu item di listing.
type WWWEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"` // dir | file | symlink
	Size       int64  `json:"size"`
	Mode       string `json:"mode"` // -rwxr-xr-x
	Perm       string `json:"perm"` // 755
	ModTime    int64  `json:"mtime"`
	Owner      string `json:"owner"`
	Group      string `json:"group"`
	LinkTarget string `json:"linkTarget,omitempty"`
	LinkDir    bool   `json:"linkDir,omitempty"`
	LinkInside bool   `json:"linkInside,omitempty"`
	Hidden     bool   `json:"hidden"`
	Writable   bool   `json:"writable"`
}

func (s *WWWService) entryFor(abs string, info fs.FileInfo) WWWEntry {
	e := WWWEntry{
		Name:    info.Name(),
		Path:    s.toRel(abs),
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		Perm:    fmt.Sprintf("%03o", info.Mode().Perm()),
		ModTime: info.ModTime().Unix(),
		Hidden:  strings.HasPrefix(info.Name(), "."),
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		e.Type = "symlink"
		if t, err := os.Readlink(abs); err == nil {
			e.LinkTarget = t
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			e.LinkInside = s.within(real)
			if st, err := os.Stat(real); err == nil {
				e.LinkDir = st.IsDir()
				if !st.IsDir() {
					e.Size = st.Size()
				}
			}
		}
	case info.IsDir():
		e.Type = "dir"
		e.Size = 0
	default:
		e.Type = "file"
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		e.Owner, e.Group = s.idNames(st.Uid, st.Gid)
	}
	e.Writable = syscall.Access(abs, 2 /* W_OK */) == nil
	return e
}

// WWWListing adalah isi satu folder.
type WWWListing struct {
	Root    string     `json:"root"`
	Path    string     `json:"path"`
	Entries []WWWEntry `json:"entries"`
	Parent  string     `json:"parent,omitempty"`
}

func (s *WWWService) List(rel string) (*WWWListing, error) {
	dir, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, ErrWWWNotFound
	}
	if !st.IsDir() {
		return nil, ErrWWWNotDir
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]WWWEntry, 0, len(items))
	for _, it := range items {
		info, err := it.Info()
		if err != nil {
			continue
		}
		out = append(out, s.entryFor(filepath.Join(dir, it.Name()), info))
	}
	sort.SliceStable(out, func(i, j int) bool {
		di := out[i].Type == "dir" || (out[i].Type == "symlink" && out[i].LinkDir)
		dj := out[j].Type == "dir" || (out[j].Type == "symlink" && out[j].LinkDir)
		if di != dj {
			return di
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	l := &WWWListing{Root: s.root, Path: s.toRel(dir), Entries: out}
	if l.Path != "/" {
		l.Parent = filepath.ToSlash(filepath.Dir(l.Path))
	}
	return l, nil
}

// Stat mengembalikan info satu entri.
func (s *WWWService) Stat(rel string) (*WWWEntry, error) {
	abs, err := s.resolveEntry(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, ErrWWWNotFound
	}
	e := s.entryFor(abs, info)
	return &e, nil
}

// ─── Baca / tulis ──────────────────────────────────────────────────────────

// WWWFile adalah isi file untuk editor.
type WWWFile struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Size     int64  `json:"size"`
	ModTime  int64  `json:"mtime"`
	Perm     string `json:"perm"`
	Writable bool   `json:"writable"`
}

func (s *WWWService) Read(rel string) (*WWWFile, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, ErrWWWNotFound
	}
	if st.IsDir() {
		return nil, ErrWWWIsDir
	}
	if st.Size() > wwwEditorMax {
		return nil, ErrWWWTooLarge
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	probe := b
	if len(probe) > 8000 {
		probe = probe[:8000]
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return nil, ErrWWWBinary
	}
	return &WWWFile{
		Path: s.toRel(abs), Content: string(b), Size: st.Size(),
		ModTime: st.ModTime().UnixMilli(), Perm: fmt.Sprintf("%03o", st.Mode().Perm()),
		Writable: syscall.Access(abs, 2) == nil,
	}, nil
}

// Write menimpa isi file secara in-place (pemilik, grup & permission tetap).
// expectedMtime > 0 → tolak bila file sudah diubah pihak lain.
func (s *WWWService) Write(rel, content string, expectedMtime int64) (*WWWFile, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	if abs == s.root {
		return nil, ErrWWWRoot
	}
	st, statErr := os.Stat(abs)
	if statErr == nil {
		if st.IsDir() {
			return nil, ErrWWWIsDir
		}
		if expectedMtime > 0 && st.ModTime().UnixMilli() != expectedMtime {
			return nil, ErrWWWConflict
		}
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(f, content); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	st, err = os.Stat(abs)
	if err != nil {
		return nil, err
	}
	return &WWWFile{Path: s.toRel(abs), Size: st.Size(), ModTime: st.ModTime().UnixMilli(),
		Perm: fmt.Sprintf("%03o", st.Mode().Perm()), Writable: true}, nil
}

// Create membuat file kosong atau folder baru di dalam dir.
func (s *WWWService) Create(dirRel, name string, isDir bool) (*WWWEntry, error) {
	if !validName(name) {
		return nil, ErrWWWBadName
	}
	dir, err := s.resolve(dirRel)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(dir, name)
	if _, err := os.Lstat(target); err == nil {
		return nil, ErrWWWExists
	}
	if isDir {
		if err := os.Mkdir(target, 0o755); err != nil {
			return nil, err
		}
	} else {
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return nil, err
		}
		f.Close()
	}
	info, err := os.Lstat(target)
	if err != nil {
		return nil, err
	}
	e := s.entryFor(target, info)
	return &e, nil
}

// Rename mengganti nama satu entri (tetap di folder yang sama).
func (s *WWWService) Rename(rel, newName string) (string, error) {
	if !validName(newName) {
		return "", ErrWWWBadName
	}
	src, err := s.resolveEntry(rel)
	if err != nil {
		return "", err
	}
	if src == s.root {
		return "", ErrWWWRoot
	}
	if _, err := os.Lstat(src); err != nil {
		return "", ErrWWWNotFound
	}
	dst := filepath.Join(filepath.Dir(src), newName)
	if _, err := os.Lstat(dst); err == nil {
		return "", ErrWWWExists
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return s.toRel(dst), nil
}

// Delete menghapus entri (folder secara rekursif). Symlink dihapus linknya saja.
func (s *WWWService) Delete(rels []string) (int, error) {
	n := 0
	for _, rel := range rels {
		abs, err := s.resolveEntry(rel)
		if err != nil {
			return n, err
		}
		if abs == s.root {
			return n, ErrWWWRoot
		}
		if _, err := os.Lstat(abs); err != nil {
			continue
		}
		if err := os.RemoveAll(abs); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Chmod mengubah permission (non-rekursif; symlink dilewati).
func (s *WWWService) Chmod(rels []string, mode string, recursive bool) (int, error) {
	m, err := strconv.ParseUint(strings.TrimSpace(mode), 8, 32)
	if err != nil || m > 0o7777 || len(strings.TrimSpace(mode)) < 3 {
		return 0, ErrWWWBadMode
	}
	perm := os.FileMode(m) & (os.ModePerm | os.ModeSetgid | os.ModeSticky)
	// setuid sengaja tidak diizinkan.
	if m&0o4000 != 0 {
		return 0, ErrWWWBadMode
	}
	if m&0o2000 != 0 {
		perm |= os.ModeSetgid
	}
	if m&0o1000 != 0 {
		perm |= os.ModeSticky
	}
	n := 0
	for _, rel := range rels {
		abs, err := s.resolveEntry(rel)
		if err != nil {
			return n, err
		}
		if abs == s.root {
			return n, ErrWWWRoot
		}
		info, err := os.Lstat(abs)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if recursive && info.IsDir() {
			err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.Type()&os.ModeSymlink != 0 {
					return nil
				}
				if e := os.Chmod(p, perm); e == nil {
					n++
				}
				return nil
			})
			if err != nil {
				return n, err
			}
			continue
		}
		if err := os.Chmod(abs, perm); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ─── Salin / pindah ────────────────────────────────────────────────────────

func (s *WWWService) prepareTransfer(rel, destDirRel string) (src, dst string, err error) {
	src, err = s.resolveEntry(rel)
	if err != nil {
		return
	}
	if src == s.root {
		return "", "", ErrWWWRoot
	}
	if _, e := os.Lstat(src); e != nil {
		return "", "", ErrWWWNotFound
	}
	destDir, err := s.resolve(destDirRel)
	if err != nil {
		return
	}
	st, e := os.Stat(destDir)
	if e != nil || !st.IsDir() {
		return "", "", ErrWWWNotDir
	}
	realSrc, _ := filepath.EvalSymlinks(src)
	if realSrc != "" && (destDir == realSrc || strings.HasPrefix(destDir, realSrc+string(os.PathSeparator))) {
		return "", "", ErrWWWIntoItself
	}
	dst = filepath.Join(destDir, filepath.Base(src))
	return
}

// uniqueName menambahkan " (copy)", " (copy 2)", ... bila nama sudah ada.
func uniqueName(p string) string {
	if _, err := os.Lstat(p); err != nil {
		return p
	}
	dir, base := filepath.Dir(p), filepath.Base(p)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if strings.HasSuffix(strings.ToLower(stem), ".tar") {
		ext = stem[len(stem)-4:] + ext
		stem = stem[:len(stem)-4]
	}
	for i := 1; i < 1000; i++ {
		suffix := " (copy)"
		if i > 1 {
			suffix = fmt.Sprintf(" (copy %d)", i)
		}
		cand := filepath.Join(dir, stem+suffix+ext)
		if _, err := os.Lstat(cand); err != nil {
			return cand
		}
	}
	return p + "." + strconv.FormatInt(time.Now().Unix(), 10)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(ctx context.Context, src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(t, dst)
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()); err != nil {
			return err
		}
		items, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, it := range items {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := copyTree(ctx, filepath.Join(src, it.Name()), filepath.Join(dst, it.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		return copyFile(src, dst, info.Mode())
	}
}

// Copy menyalin entri ke folder tujuan (nama bentrok → "(copy)").
func (s *WWWService) Copy(ctx context.Context, rels []string, destDirRel string) (int, error) {
	n := 0
	for _, rel := range rels {
		src, dst, err := s.prepareTransfer(rel, destDirRel)
		if err != nil {
			return n, err
		}
		if err := copyTree(ctx, src, uniqueName(dst)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Move memindah entri ke folder tujuan (gagal bila nama sudah ada).
func (s *WWWService) Move(ctx context.Context, rels []string, destDirRel string) (int, error) {
	n := 0
	for _, rel := range rels {
		src, dst, err := s.prepareTransfer(rel, destDirRel)
		if err != nil {
			return n, err
		}
		if src == dst {
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			return n, fmt.Errorf("%w: %s", ErrWWWExists, filepath.Base(dst))
		}
		if err := os.Rename(src, dst); err != nil {
			// Beda filesystem → salin lalu hapus sumber.
			var le *os.LinkError
			if errors.As(err, &le) && errors.Is(le.Err, syscall.EXDEV) {
				if err := copyTree(ctx, src, dst); err != nil {
					return n, err
				}
				if err := os.RemoveAll(src); err != nil {
					return n, err
				}
			} else {
				return n, err
			}
		}
		n++
	}
	return n, nil
}

// ─── Upload bertahap (chunked) ─────────────────────────────────────────────
//
// Cloudflare di depan panel membatasi body 100 MB per request dan request
// panjang rawan timeout, jadi file dipecah klien menjadi potongan beberapa MB. Potongan ditulis ke file sementara tersembunyi di
// folder tujuan lalu di-rename atomik saat potongan terakhir tiba.

func uploadTempName(dst, id string) string {
	return filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".upload-"+id)
}

func validUploadID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// UploadChunk menulis satu potongan pada offset. final=true → selesaikan file.
func (s *WWWService) UploadChunk(destRel, uploadID string, offset int64, data io.Reader, final, overwrite bool) (*WWWEntry, error) {
	if !validUploadID(uploadID) {
		return nil, errors.New("uploadId tidak valid")
	}
	c := cleanRel(destRel)
	for _, part := range strings.Split(strings.Trim(c, "/"), "/") {
		if !validName(part) {
			return nil, ErrWWWBadName
		}
	}
	dst, err := s.resolveEntry(c)
	if err != nil {
		return nil, err
	}
	if dst == s.root {
		return nil, ErrWWWRoot
	}
	// Upload folder: buat subfolder yang belum ada (tetap di dalam root).
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if real, err := evalExisting(filepath.Dir(dst)); err != nil || !s.within(real) {
		return nil, ErrFileAccessDenied
	}
	if offset == 0 && !overwrite {
		if _, err := os.Lstat(dst); err == nil {
			return nil, ErrWWWExists
		}
	}
	tmp := uploadTempName(dst, uploadID)
	flags := os.O_WRONLY | os.O_CREATE
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(tmp, flags, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := io.Copy(f, data); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if !final {
		return nil, nil
	}
	if !overwrite {
		if _, err := os.Lstat(dst); err == nil {
			_ = os.Remove(tmp)
			return nil, ErrWWWExists
		}
	} else if st, err := os.Stat(dst); err == nil {
		if st.IsDir() {
			_ = os.Remove(tmp)
			return nil, ErrWWWIsDir
		}
		_ = os.Chmod(tmp, st.Mode().Perm())
	}
	if err := os.Rename(tmp, dst); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dst)
	if err != nil {
		return nil, err
	}
	e := s.entryFor(dst, info)
	return &e, nil
}

// AbortUpload membuang file sementara upload yang dibatalkan.
func (s *WWWService) AbortUpload(destRel, uploadID string) {
	if !validUploadID(uploadID) {
		return
	}
	dst, err := s.resolveEntry(destRel)
	if err != nil {
		return
	}
	_ = os.Remove(uploadTempName(dst, uploadID))
}

// ─── Download ──────────────────────────────────────────────────────────────

// OpenForDownload membuka file untuk dikirim; folder dikembalikan isDir=true.
func (s *WWWService) OpenForDownload(rel string) (abs string, info fs.FileInfo, err error) {
	abs, err = s.resolve(rel)
	if err != nil {
		return
	}
	info, err = os.Stat(abs)
	if err != nil {
		err = ErrWWWNotFound
	}
	return
}

// ZipTo menulis isi folder/berkas sebagai zip ke w (streaming).
func (s *WWWService) ZipTo(ctx context.Context, w io.Writer, absPaths []string) error {
	zw := zip.NewWriter(w)
	for _, abs := range absPaths {
		base := filepath.Dir(abs)
		err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil // jangan ikuti symlink (bisa keluar root)
			}
			rel, _ := filepath.Rel(base, p)
			rel = filepath.ToSlash(rel)
			info, err := d.Info()
			if err != nil {
				return nil
			}
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return nil
			}
			hdr.Name = rel
			if d.IsDir() {
				hdr.Name += "/"
				_, err = zw.CreateHeader(hdr)
				return err
			}
			hdr.Method = zip.Deflate
			fw, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return nil
			}
			_, err = io.Copy(fw, f)
			f.Close()
			return err
		})
		if err != nil {
			zw.Close()
			return err
		}
	}
	return zw.Close()
}

// Compress membuat arsip zip berisi entri terpilih di folder dirRel.
func (s *WWWService) Compress(ctx context.Context, rels []string, dirRel, name string) (*WWWEntry, error) {
	if !strings.HasSuffix(strings.ToLower(name), ".zip") {
		name += ".zip"
	}
	if !validName(name) {
		return nil, ErrWWWBadName
	}
	dir, err := s.resolve(dirRel)
	if err != nil {
		return nil, err
	}
	var abs []string
	for _, rel := range rels {
		a, err := s.resolveEntry(rel)
		if err != nil {
			return nil, err
		}
		if a == s.root {
			return nil, ErrWWWRoot
		}
		abs = append(abs, a)
	}
	target := uniqueName(filepath.Join(dir, name))
	tmp := target + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	bw := bufio.NewWriterSize(f, 256*1024)
	if err := s.ZipTo(ctx, bw, abs); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, target); err != nil {
		return nil, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return nil, err
	}
	e := s.entryFor(target, info)
	return &e, nil
}

// Extract membongkar arsip (zip / tar / tar.gz) ke folder destRel.
// Entri yang mencoba keluar folder tujuan (zip-slip) dan symlink dilewati.
func (s *WWWService) Extract(ctx context.Context, archiveRel, destRel string, overwrite bool) (int, error) {
	src, err := s.resolve(archiveRel)
	if err != nil {
		return 0, err
	}
	dest, err := s.resolve(destRel)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return 0, err
	}
	lower := strings.ToLower(src)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return s.extractZip(ctx, src, dest, overwrite)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		f, err := os.Open(src)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return 0, err
		}
		defer gz.Close()
		return s.extractTar(ctx, gz, dest, overwrite)
	case strings.HasSuffix(lower, ".tar"):
		f, err := os.Open(src)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		return s.extractTar(ctx, f, dest, overwrite)
	}
	return 0, ErrWWWUnsupport
}

func (s *WWWService) safeTarget(dest, name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	target := filepath.Join(dest, filepath.Clean("/"+name))
	if target == dest || !strings.HasPrefix(target, dest+string(os.PathSeparator)) {
		return "", false
	}
	if real, err := evalExisting(filepath.Dir(target)); err != nil || !s.within(real) {
		return "", false
	}
	return target, true
}

func sanitizePerm(m os.FileMode, dir bool) os.FileMode {
	p := m.Perm()
	if p == 0 {
		if dir {
			return 0o755
		}
		return 0o644
	}
	return p | 0o200 // pastikan pemilik tetap bisa menulis
}

func writeExtracted(target string, r io.Reader, mode os.FileMode, overwrite bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if st, err := os.Lstat(target); err == nil && st.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(target); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(target, flags, mode)
	if err != nil {
		if !overwrite && errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *WWWService) extractZip(ctx context.Context, src, dest string, overwrite bool) (int, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	n := 0
	for _, f := range zr.File {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		target, ok := s.safeTarget(dest, f.Name)
		if !ok {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, sanitizePerm(f.Mode(), true)); err != nil {
				return n, err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return n, err
		}
		err = writeExtracted(target, rc, sanitizePerm(f.Mode(), false), overwrite)
		rc.Close()
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *WWWService) extractTar(ctx context.Context, r io.Reader, dest string, overwrite bool) (int, error) {
	tr := tar.NewReader(r)
	n := 0
	for {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		target, ok := s.safeTarget(dest, hdr.Name)
		if !ok {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, sanitizePerm(os.FileMode(hdr.Mode), true)); err != nil {
				return n, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := writeExtracted(target, tr, sanitizePerm(os.FileMode(hdr.Mode), false), overwrite); err != nil {
				return n, err
			}
			n++
		}
	}
}

// ─── Pencarian & ukuran ────────────────────────────────────────────────────

var wwwSkipDirs = map[string]bool{"node_modules": true, "vendor": true, ".git": true, ".cache": true}

// Search mencari nama file/folder (case-insensitive) di bawah rel.
func (s *WWWService) Search(ctx context.Context, rel, q string, limit int) ([]WWWEntry, bool, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, false, errors.New("kata kunci kosong")
	}
	base, err := s.resolve(rel)
	if err != nil {
		return nil, false, err
	}
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	var out []WWWEntry
	truncated := false
	errStop := errors.New("stop")
	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			truncated = true
			return errStop
		}
		if d.IsDir() && p != base && wwwSkipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if p != base && strings.Contains(strings.ToLower(d.Name()), q) {
			if info, err := d.Info(); err == nil {
				out = append(out, s.entryFor(p, info))
			}
			if len(out) >= limit {
				truncated = true
				return errStop
			}
		}
		return nil
	})
	if err != nil && err != errStop {
		return out, truncated, err
	}
	return out, truncated, nil
}

// DiskUsage menghitung total ukuran folder (dibatasi oleh ctx).
func (s *WWWService) DiskUsage(ctx context.Context, rel string) (int64, int, bool, error) {
	base, err := s.resolve(rel)
	if err != nil {
		return 0, 0, false, err
	}
	var total int64
	files := 0
	partial := false
	errStop := errors.New("stop")
	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			partial = true
			return errStop
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				if st, ok := info.Sys().(*syscall.Stat_t); ok {
					total += st.Blocks * 512
				} else {
					total += info.Size()
				}
				files++
			}
		}
		return nil
	})
	if err != nil && err != errStop {
		return total, files, partial, err
	}
	return total, files, partial, nil
}
