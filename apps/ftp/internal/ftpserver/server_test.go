package ftpserver

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestServerStartsAndStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	s := &Server{Addr: ":0", Port: port, PassivePortRange: [2]int{40000, 40099}}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop() }()
	time.Sleep(50 * time.Millisecond)
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
}

// TestAuthTLSRejectedWhenNotConfigured locks in the fix for the AUTH TLS nil-config
// crash. ftpserverlib v0.24.1 handleAUTH calls tls.Server(conn, nil) when
// GetTLSConfig returns (nil, nil) and the next read panics in readClientHello.
// Our GetTLSConfig returns an error when Server.TLS == nil, so handleAUTH must
// reply with a 5xx and leave the connection plaintext. Regression guard.
func TestAuthTLSRejectedWhenNotConfigured(t *testing.T) {
	s := &Server{Addr: "127.0.0.1:0", PassivePortRange: [2]int{40000, 40099}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop() }()

	conn, err := net.DialTimeout("tcp", s.BoundAddr(), time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", s.BoundAddr(), err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	r := bufio.NewReader(conn)
	// Banner
	if _, err := r.ReadString('\n'); err != nil {
		t.Fatalf("banner read: %v", err)
	}
	if _, err := conn.Write([]byte("AUTH TLS\r\n")); err != nil {
		t.Fatalf("write AUTH TLS: %v", err)
	}
	reply, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("AUTH TLS reply: %v", err)
	}
	if strings.HasPrefix(reply, "234 ") {
		t.Fatalf("server accepted AUTH TLS without certs configured: %q", reply)
	}
	if !strings.HasPrefix(reply, "5") && !strings.HasPrefix(reply, "4") {
		t.Fatalf("expected 4xx/5xx reply, got %q", reply)
	}
	// Connection must still be plaintext — a follow-up command should work.
	if _, err := conn.Write([]byte("QUIT\r\n")); err != nil {
		t.Fatalf("write QUIT: %v", err)
	}
	if _, err := r.ReadString('\n'); err != nil {
		t.Fatalf("QUIT reply: %v", err)
	}
}
