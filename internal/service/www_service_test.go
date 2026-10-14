package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestWWW(t *testing.T) (*WWWService, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "www")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside, filepath.Join(root, "app", "public")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must(t, os.WriteFile(filepath.Join(root, "app", "artisan"), []byte("#!/usr/bin/env php"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "app", "public", "index.php"), []byte("<?php echo 1;"), 0o644))
	must(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("rahasia"), 0o600))
	must(t, os.Symlink(outside, filepath.Join(root, "escape")))
	must(t, os.Symlink(filepath.Join(root, "app"), filepath.Join(root, "inner-link")))
	return NewWWWService(root, filepath.Join(base, "nginx")), root, outside
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestWWWTraversalBlocked(t *testing.T) {
	s, _, _ := newTestWWW(t)
	// ".." dinormalkan ke root, tidak pernah keluar.
	l, err := s.List("/../../..")
	must(t, err)
	if l.Path != "/" {
		t.Fatalf("traversal tidak dinormalkan: %q", l.Path)
	}
	if _, err := s.Read("/escape/secret.txt"); !errors.Is(err, ErrFileAccessDenied) {
		t.Fatalf("symlink ke luar root harus ditolak, dapat: %v", err)
	}
	if _, err := s.List("/escape"); !errors.Is(err, ErrFileAccessDenied) {
		t.Fatalf("listing via symlink luar harus ditolak, dapat: %v", err)
	}
	if _, err := s.Write("/escape/new.txt", "x", 0); !errors.Is(err, ErrFileAccessDenied) {
		t.Fatalf("tulis via symlink luar harus ditolak, dapat: %v", err)
	}
	// Symlink yang menunjuk ke dalam root boleh diikuti.
	if _, err := s.List("/inner-link"); err != nil {
		t.Fatalf("symlink dalam root harus boleh: %v", err)
	}
}

func TestWWWDeleteSymlinkKeepsTarget(t *testing.T) {
	s, root, outside := newTestWWW(t)
	n, err := s.Delete([]string{"/escape"})
	must(t, err)
	if n != 1 {
		t.Fatalf("harus menghapus 1 link, dapat %d", n)
	}
	if _, err := os.Lstat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatal("link masih ada")
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatal("target symlink di luar root ikut terhapus!")
	}
	if _, err := s.Delete([]string{"/"}); !errors.Is(err, ErrWWWRoot) {
		t.Fatalf("hapus root harus ditolak, dapat %v", err)
	}
}

func TestWWWWriteConflictAndPerms(t *testing.T) {
	s, root, _ := newTestWWW(t)
	f, err := s.Read("/app/public/index.php")
	must(t, err)
	must(t, os.Chmod(filepath.Join(root, "app/public/index.php"), 0o640))
	f2, err := s.Write("/app/public/index.php", "<?php echo 2;", f.ModTime)
	if err != nil && !errors.Is(err, ErrWWWConflict) {
		t.Fatal(err)
	}
	if err == nil && f2.Perm != "640" {
		t.Fatalf("permission harus tetap 640, dapat %s", f2.Perm)
	}
	if _, err := s.Write("/app/public/index.php", "lama", 12345); !errors.Is(err, ErrWWWConflict) {
		t.Fatalf("mtime berbeda harus konflik, dapat %v", err)
	}
}

func TestWWWChunkedUpload(t *testing.T) {
	s, root, _ := newTestWWW(t)
	id := "abc12345-upload"
	_, err := s.UploadChunk("/app/data/big.bin", id, 0, strings.NewReader("hello "), false, false)
	must(t, err)
	e, err := s.UploadChunk("/app/data/big.bin", id, 6, strings.NewReader("world"), true, false)
	must(t, err)
	b, _ := os.ReadFile(filepath.Join(root, "app/data/big.bin"))
	if string(b) != "hello world" || e.Size != 11 {
		t.Fatalf("isi upload salah: %q", b)
	}
	// Tidak boleh menimpa tanpa overwrite.
	if _, err := s.UploadChunk("/app/data/big.bin", "zzz99999", 0, strings.NewReader("x"), true, false); !errors.Is(err, ErrWWWExists) {
		t.Fatalf("harus ditolak karena sudah ada, dapat %v", err)
	}
	// Upload lewat symlink ke luar root ditolak.
	if _, err := s.UploadChunk("/escape/evil.sh", "yyy99999", 0, strings.NewReader("x"), true, false); !errors.Is(err, ErrFileAccessDenied) {
		t.Fatalf("upload ke luar root harus ditolak, dapat %v", err)
	}
	if _, err := s.UploadChunk("/app/x", "../../etc", 0, strings.NewReader("x"), true, false); err == nil {
		t.Fatal("uploadId berbahaya harus ditolak")
	}
}

func TestWWWZipSlipAndRoundtrip(t *testing.T) {
	s, root, _ := newTestWWW(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"ok/readme.txt":        "aman",
		"../../pwned.txt":      "jahat",
		"/abs/also-inside.txt": "ok",
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	must(t, zw.Close())
	must(t, os.WriteFile(filepath.Join(root, "app", "arsip.zip"), buf.Bytes(), 0o644))

	n, err := s.Extract(context.Background(), "/app/arsip.zip", "/app/out", false)
	must(t, err)
	if _, err := os.Stat(filepath.Join(root, "app/out/ok/readme.txt")); err != nil {
		t.Fatal("entri normal tidak terekstrak")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "pwned.txt")); err == nil {
		t.Fatal("ZIP-SLIP: file keluar dari folder tujuan!")
	}
	if n < 1 {
		t.Fatalf("jumlah ekstrak tidak wajar: %d", n)
	}

	e, err := s.Compress(context.Background(), []string{"/app/public"}, "/app", "backup")
	must(t, err)
	if !strings.HasSuffix(e.Name, ".zip") {
		t.Fatalf("nama arsip harus .zip: %s", e.Name)
	}
}

func TestWWWCopyMoveGuards(t *testing.T) {
	s, root, _ := newTestWWW(t)
	if _, err := s.Copy(context.Background(), []string{"/app"}, "/app/public"); !errors.Is(err, ErrWWWIntoItself) {
		t.Fatalf("salin ke dalam diri sendiri harus ditolak, dapat %v", err)
	}
	must(t, os.MkdirAll(filepath.Join(root, "dest"), 0o755))
	n, err := s.Copy(context.Background(), []string{"/app/public/index.php"}, "/app/public")
	must(t, err)
	if n != 1 {
		t.Fatal("copy gagal")
	}
	if _, err := os.Stat(filepath.Join(root, "app/public/index (copy).php")); err != nil {
		t.Fatal("nama duplikat harus jadi '(copy)'")
	}
	_, err = s.Move(context.Background(), []string{"/app/public/index (copy).php"}, "/dest")
	must(t, err)
	if _, err := os.Stat(filepath.Join(root, "dest/index (copy).php")); err != nil {
		t.Fatal("move gagal")
	}
}

func TestWWWChmodRejectsSetuid(t *testing.T) {
	s, _, _ := newTestWWW(t)
	if _, err := s.Chmod([]string{"/app/artisan"}, "4755", false); !errors.Is(err, ErrWWWBadMode) {
		t.Fatalf("setuid harus ditolak, dapat %v", err)
	}
	if _, err := s.Chmod([]string{"/app/artisan"}, "750", false); err != nil {
		t.Fatal(err)
	}
}

func TestWWWSitesDetection(t *testing.T) {
	s, root, _ := newTestWWW(t)
	nginx := filepath.Join(filepath.Dir(root), "nginx")
	must(t, os.MkdirAll(nginx, 0o755))
	conf := `server {
    listen 443 ssl;
    server_name app.example.com www.app.example.com;
    root ` + filepath.Join(root, "app", "public") + `;
    location ~ \.php$ { fastcgi_pass unix:/var/run/php/php8.3-fpm.sock; }
}`
	must(t, os.WriteFile(filepath.Join(nginx, "app"), []byte(conf), 0o644))
	sites := s.Sites(context.Background(), nil, nil)
	var found *Website
	for i := range sites {
		if sites[i].Path == "/app" {
			found = &sites[i]
		}
	}
	if found == nil {
		t.Fatalf("situs /app tidak terdeteksi: %+v", sites)
	}
	if found.Framework != "Laravel" || !found.SSL || len(found.Domains) != 2 || found.PHP != "PHP 8.3 FPM" || found.Runtime != "nginx" {
		t.Fatalf("deteksi salah: %+v", found)
	}
}
