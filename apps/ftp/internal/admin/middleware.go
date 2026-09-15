package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

type SessionManager struct {
	mu           sync.Mutex
	ttl          time.Duration
	cookieSecure bool
	sessions     map[string]sessionEntry
}

type sessionEntry struct {
	username   string
	role       string
	authMethod string
	email      string
	firstName  string
	lastName   string
	expires    time.Time
}

type ctxKey int

const (
	usernameCtxKey ctxKey = iota
	roleCtxKey
	authMethodCtxKey
	emailCtxKey
	firstNameCtxKey
	lastNameCtxKey
)

// CurrentUser returns the username of the authenticated caller, set by
// SessionManager.Authenticate. Empty if called outside an authenticated route.
func CurrentUser(r *http.Request) string {
	u, _ := r.Context().Value(usernameCtxKey).(string)
	return u
}

// CurrentRole returns the caller's role ("admin" or "readonly").
func CurrentRole(r *http.Request) string {
	role, _ := r.Context().Value(roleCtxKey).(string)
	return role
}

// CurrentAuthMethod returns how the caller authenticated ("local" or "oidc").
func CurrentAuthMethod(r *http.Request) string {
	m, _ := r.Context().Value(authMethodCtxKey).(string)
	return m
}

// CurrentEmail returns the authenticated caller's email, as reported by OIDC.
func CurrentEmail(r *http.Request) string {
	e, _ := r.Context().Value(emailCtxKey).(string)
	return e
}

// CurrentFirstName returns the authenticated caller's given name, as reported by OIDC.
func CurrentFirstName(r *http.Request) string {
	n, _ := r.Context().Value(firstNameCtxKey).(string)
	return n
}

// CurrentLastName returns the authenticated caller's family name, as reported by OIDC.
func CurrentLastName(r *http.Request) string {
	n, _ := r.Context().Value(lastNameCtxKey).(string)
	return n
}

func NewSessionManager(ttl time.Duration, cookieSecure bool) *SessionManager {
	return &SessionManager{ttl: ttl, cookieSecure: cookieSecure, sessions: map[string]sessionEntry{}}
}

func (s *SessionManager) Issue(username, role, authMethod, email, firstName, lastName string) (*http.Cookie, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[tok] = sessionEntry{username: username, role: role, authMethod: authMethod, email: email, firstName: firstName, lastName: lastName, expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return &http.Cookie{Name: "session", Value: tok, Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode}, nil
}

// Revoke invalidates a session token immediately (sign-out).
func (s *SessionManager) Revoke(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// ClearCookie returns a cookie that, when set, deletes the browser's session cookie.
func (s *SessionManager) ClearCookie() *http.Cookie {
	return &http.Cookie{Name: "session", Value: "", Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1}
}

func (s *SessionManager) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("session")
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.mu.Lock()
		entry, ok := s.sessions[c.Value]
		if ok && entry.expires.After(time.Now()) {
			delete(s.sessions, c.Value)
			entry.expires = time.Now().Add(s.ttl)
			s.sessions[c.Value] = entry
		} else {
			ok = false
		}
		s.mu.Unlock()
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), usernameCtxKey, entry.username)
		ctx = context.WithValue(ctx, roleCtxKey, entry.role)
		ctx = context.WithValue(ctx, authMethodCtxKey, entry.authMethod)
		ctx = context.WithValue(ctx, emailCtxKey, entry.email)
		ctx = context.WithValue(ctx, firstNameCtxKey, entry.firstName)
		ctx = context.WithValue(ctx, lastNameCtxKey, entry.lastName)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole rejects callers whose session role doesn't match. Must run
// after Authenticate so the role is present in the request context.
func (a *API) RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if CurrentRole(r) != role {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
