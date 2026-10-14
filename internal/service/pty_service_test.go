package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPtyRoundtripAndReplay(t *testing.T) {
	m := NewPtyManager()
	defer m.Shutdown()
	s, err := m.Create(100, 30, t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte("echo pty-$((40+2))\n")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var got strings.Builder
	_, _ = s.Stream(ctx, 0, time.Second, func(ch PtyChunk) error {
		got.Write(ch.Data)
		if strings.Contains(got.String(), "pty-42") {
			cancel()
		}
		return nil
	}, nil)
	if !strings.Contains(got.String(), "pty-42") {
		t.Fatalf("output shell tidak diterima: %q", got.String())
	}

	// Penyambungan ulang dari offset 0 harus memutar ulang output yang sama.
	chunk, _, _ := s.ReadFrom(0, 0)
	if !strings.Contains(string(chunk.Data), "pty-42") {
		t.Fatal("buffer replay tidak berisi output sebelumnya")
	}

	if err := s.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	// Shell keluar → Stream berakhir dengan exited=true.
	_ = s.Write([]byte("exit 3\n"))
	ctx2, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel2()
	exited, _ := s.Stream(ctx2, chunk.Offset+int64(len(chunk.Data)), time.Second, func(PtyChunk) error { return nil }, nil)
	if !exited || s.ExitCode() != 3 {
		t.Fatalf("exit tidak terdeteksi: exited=%v code=%d", exited, s.ExitCode())
	}
	if err := m.Close(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(s.ID); err == nil {
		t.Fatal("sesi masih ada setelah ditutup")
	}
}

func TestPtyCleanEnvHasNoSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "jangan-bocor")
	t.Setenv("DB_PASSWORD", "jangan-bocor")
	m := &PtyManager{user: "u", home: "/tmp", shell: "/bin/sh"}
	for _, kv := range m.cleanEnv() {
		if strings.Contains(kv, "jangan-bocor") {
			t.Fatalf("secret service bocor ke shell: %s", kv)
		}
	}
}
