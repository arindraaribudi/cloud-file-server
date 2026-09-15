package ftpserver

import (
	"sync"

	"github.com/google/uuid"
)

type SessionRegistry struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]*session
}

type session struct {
	ID       uuid.UUID
	Username string
	ClientIP string
	TermCh   chan struct{}
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{sessions: map[uuid.UUID]*session{}}
}

func (r *SessionRegistry) Register(username, clientIP string) uuid.UUID {
	id := uuid.New()
	r.mu.Lock()
	r.sessions[id] = &session{ID: id, Username: username, ClientIP: clientIP, TermCh: make(chan struct{})}
	r.mu.Unlock()
	return id
}

func (r *SessionRegistry) IsActive(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	return ok && s != nil
}

func (r *SessionRegistry) Terminate(id uuid.UUID) {
	r.mu.Lock()
	s, ok := r.sessions[id]
	if ok {
		close(s.TermCh)
		delete(r.sessions, id)
	}
	r.mu.Unlock()
}

func (r *SessionRegistry) TermChannel(id uuid.UUID) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		return s.TermCh
	}
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (r *SessionRegistry) ActiveFor(username string) []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []uuid.UUID{}
	for id, s := range r.sessions {
		if s.Username == username {
			out = append(out, id)
		}
	}
	return out
}

func (r *SessionRegistry) ActiveCount(username string) int {
	return len(r.ActiveFor(username))
}
