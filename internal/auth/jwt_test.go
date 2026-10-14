package auth

import (
	"testing"
	"time"
)

func TestTicketCannotBeUsedAsSession(t *testing.T) {
	s := NewService("rahasia-uji", time.Hour)
	tk, err := s.IssueTicket("www-dl", "/app/.env", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(tk); err == nil {
		t.Fatal("tiket download tidak boleh diterima sebagai token sesi")
	}
	c, err := s.VerifyTicket(tk, "www-dl")
	if err != nil || c.Subject != "/app/.env" {
		t.Fatalf("tiket valid ditolak: %v", err)
	}
	if _, err := s.VerifyTicket(tk, "pty-ws"); err == nil {
		t.Fatal("tiket untuk tujuan lain harus ditolak")
	}
}

func TestSessionCannotBeUsedAsTicket(t *testing.T) {
	s := NewService("rahasia-uji", time.Hour)
	sess, _ := s.IssueAdmin(1)
	if _, err := s.Verify(sess); err != nil {
		t.Fatalf("token sesi admin harus valid: %v", err)
	}
	if _, err := s.VerifyTicket(sess, "www-dl"); err == nil {
		t.Fatal("token sesi tidak boleh dipakai sebagai tiket")
	}
}

func TestTicketExpires(t *testing.T) {
	s := NewService("rahasia-uji", time.Hour)
	tk, _ := s.IssueTicket("pty-ws", "abc", false, -time.Second)
	if _, err := s.VerifyTicket(tk, "pty-ws"); err == nil {
		t.Fatal("tiket kedaluwarsa harus ditolak")
	}
}
