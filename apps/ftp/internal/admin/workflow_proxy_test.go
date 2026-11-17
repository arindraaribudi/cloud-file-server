package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWorkflowProxyInjectsIdentityAndStripsClientHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"user":        r.Header.Get("X-Authenticated-User"),
			"role":        r.Header.Get("X-Authenticated-Role"),
			"email":       r.Header.Get("X-Authenticated-Email"),
			"proxy_token": r.Header.Get("X-Workflow-Proxy-Token"),
			"method":      r.Method,
			"path":        r.URL.Path,
			"query":       r.URL.RawQuery,
		})
	}))
	defer upstream.Close()

	api := &API{
		Sessions:             NewSessionManager(time.Hour, false),
		WorkflowAPIURL:       upstream.URL,
		WorkflowServiceToken: "svc-token",
	}
	cookie, err := api.Sessions.Issue("alice", "admin", "local", "alice@example.com", "Alice", "A")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows?foo=bar", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-Authenticated-User", "attacker")
	req.Header.Set("X-Workflow-Proxy-Token", "wrong")
	w := httptest.NewRecorder()
	api.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["user"] != "alice" || got["role"] != "admin" || got["email"] != "alice@example.com" {
		t.Errorf("identity=%+v", got)
	}
	if got["proxy_token"] != "svc-token" {
		t.Errorf("proxy_token=%q", got["proxy_token"])
	}
	if got["path"] != "/api/v1/workflows" || got["query"] != "foo=bar" || got["method"] != http.MethodGet {
		t.Errorf("request shape=%+v", got)
	}
}

func TestWorkflowProxyRoleGate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	api := &API{
		Sessions:             NewSessionManager(time.Hour, false),
		WorkflowAPIURL:       upstream.URL,
		WorkflowServiceToken: "svc-token",
	}
	readonly, err := api.Sessions.Issue("bob", "readonly", "local", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := api.Sessions.Issue("alice", "admin", "local", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	handler := api.Routes()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", nil)
	req.AddCookie(readonly)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("readonly POST status=%d, want 403", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workflows", nil)
	req.AddCookie(admin)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("admin POST status=%d, want 204", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/workflow-components?stage=trigger", nil)
	req.AddCookie(readonly)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("readonly GET catalog status=%d, want 200", w.Code)
	}
}

func TestWorkflowProxyUnconfigured(t *testing.T) {
	api := &API{Sessions: NewSessionManager(time.Hour, false)}
	cookie, err := api.Sessions.Issue("alice", "admin", "local", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	api.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}
