package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionAuth(t *testing.T) {
	sm := NewSessionManager(time.Hour, false)
	h := sm.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, _ := http.Get(srv.URL)
	if resp.StatusCode != 401 {
		t.Errorf("no-cookie status=%d", resp.StatusCode)
	}

	cookie, err := sm.Issue("admin1", "admin", "local", "admin1@example.com", "Admin", "One")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.AddCookie(cookie)
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != 200 {
		t.Errorf("with-cookie status=%d", resp2.StatusCode)
	}
}
