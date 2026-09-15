package auth

import (
	"errors"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const (
	bcryptCost          = 12
	errWeakPassword     = "password must be at least 8 characters and include an uppercase letter, a lowercase letter, and a digit"
)

func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	return string(b), err
}

func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ValidatePassword enforces the minimum password complexity rule: 8+ chars,
// at least one uppercase letter, one lowercase letter, and one digit.
func ValidatePassword(plain string) error {
	if len(plain) < 8 {
		return errors.New(errWeakPassword)
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range plain {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return errors.New(errWeakPassword)
	}
	return nil
}

// Lockout tracks recent failed authentication attempts per username and denies
// access once the count reaches the configured limit within the window.
// Callers must call RecordFailure on every bad auth attempt and Reset on
// every successful authentication.
type Lockout struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	failures map[string][]time.Time
}

func NewLockout(limit int, window time.Duration) *Lockout {
	return &Lockout{limit: limit, window: window, failures: map[string][]time.Time{}}
}

func (l *Lockout) Allow(user string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.window)
	fs := l.failures[user][:0]
	for _, t := range l.failures[user] {
		if t.After(cut) {
			fs = append(fs, t)
		}
	}
	l.failures[user] = fs
	return len(fs) < l.limit
}

func (l *Lockout) RecordFailure(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[user] = append(l.failures[user], time.Now())
}

func (l *Lockout) Reset(user string) {
	l.mu.Lock()
	delete(l.failures, user)
	l.mu.Unlock()
}
