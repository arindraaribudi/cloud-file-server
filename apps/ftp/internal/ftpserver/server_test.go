package ftpserver

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestServerStartsAndStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s := &Server{Addr: ":0", Port: port, PassivePortRange: [2]int{40000, 40099}}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	time.Sleep(50 * time.Millisecond)
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
}
