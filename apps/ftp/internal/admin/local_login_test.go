package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/cos-ftp-server/internal/auth"
)

// A database outage must read as "unavailable", not "wrong password", and must
// not count toward the lockout.
func TestLocalLoginDatabaseDownIsUnavailableNotUnauthorized(t *testing.T) {
	// Nothing listens on this port; the pool connects lazily, so the first query fails.
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	a := &API{Pool: pool, LocalLogin: true, LoginLockout: auth.NewLockout(5, time.Minute)}
	for i := 0; i < 8; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"localadmin","password":"whatever"}`))
		w := httptest.NewRecorder()
		a.localLogin(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("attempt %d: status=%d body=%s, want 503", i+1, w.Code, w.Body.String())
		}
	}
	if !a.LoginLockout.Allow("localadmin") {
		t.Fatal("database errors must not count as failed logins")
	}
}
