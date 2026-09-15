package ftpserver

import (
	"testing"

	"github.com/google/uuid"
)

func TestSessionRegistryTerminate(t *testing.T) {
	r := NewSessionRegistry()
	id := r.Register("alice", "10.0.0.1")
	if !r.IsActive(id) {
		t.Fatal("session should be active")
	}
	r.Terminate(id)
	if r.IsActive(id) {
		t.Fatal("session should be terminated")
	}
	if r.ActiveCount("alice") != 0 {
		t.Fatalf("active count=%d", r.ActiveCount("alice"))
	}
}

func TestSessionRegistryTermChannel(t *testing.T) {
	r := NewSessionRegistry()
	id := r.Register("alice", "10.0.0.1")
	ch := r.TermChannel(id)
	select {
	case <-ch:
		t.Fatal("should not be terminated yet")
	default:
	}
	r.Terminate(id)
	<-ch // should unblock now
}

func TestSessionRegistryActiveFor(t *testing.T) {
	r := NewSessionRegistry()
	id1 := r.Register("alice", "10.0.0.1")
	id2 := r.Register("alice", "10.0.0.2")
	id3 := r.Register("bob", "10.0.0.3")
	defer func() { _ = id1 }()
	defer func() { _ = id2 }()
	defer func() { _ = id3 }()

	active := r.ActiveFor("alice")
	if len(active) != 2 {
		t.Fatalf("alice active=%d want 2", len(active))
	}
	if r.ActiveCount("bob") != 1 {
		t.Fatalf("bob active=%d want 1", r.ActiveCount("bob"))
	}
}

var _ = uuid.Nil // silence unused
