package admin

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// workflowRoutes proxies the workflow service API through the existing admin
// session. Browser requests stay same-origin, and the workflow service only
// needs to trust the proxy token plus identity headers.
func (a *API) workflowRoutes(r chi.Router) {
	proxy := a.workflowProxy()
	gate := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead && CurrentRole(req) != "admin" {
			writeJSONError(w, http.StatusForbidden, codeForbidden, `admin role required`)
			return
		}
		proxy.ServeHTTP(w, req)
	})

	r.Group(func(r chi.Router) {
		r.Use(a.Sessions.Authenticate)
		r.Handle("/api/v1/workflows", gate)
		r.Handle("/api/v1/workflows/*", gate)
		r.Handle("/api/v1/workflow-components", gate)
	})
}

func (a *API) workflowProxy() http.Handler {
	if strings.TrimSpace(a.WorkflowAPIURL) == "" || a.WorkflowServiceToken == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "workflow service not configured")
		})
	}
	target, err := url.Parse(a.WorkflowAPIURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSONError(w, http.StatusServiceUnavailable, codeUnavailable, "workflow service URL is invalid")
		})
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		for _, h := range []string{
			"X-Authenticated-User", "X-Authenticated-Role", "X-Authenticated-Email",
			"X-Workflow-Proxy-Token",
		} {
			req.Header.Del(h)
		}
		req.Header.Set("X-Authenticated-User", CurrentUser(req))
		req.Header.Set("X-Authenticated-Role", CurrentRole(req))
		req.Header.Set("X-Authenticated-Email", CurrentEmail(req))
		req.Header.Set("X-Workflow-Proxy-Token", a.WorkflowServiceToken)
	}
	proxy.Transport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 20 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeJSONError(w, http.StatusBadGateway, codeUnavailable, "workflow service unavailable")
	}
	return proxy
}
