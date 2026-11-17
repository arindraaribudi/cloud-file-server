package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/engine"
	"github.com/example/cos-workflow/internal/model"
	"github.com/example/cos-workflow/internal/store"
)

// WorkflowStore is the persistence surface used by the HTTP API. store.Store
// implements it; tests can inject a fake.
type WorkflowStore interface {
	List(ctx context.Context, search string, enabled *bool, limit, offset int) ([]*model.Workflow, error)
	Get(ctx context.Context, id uuid.UUID) (*model.Workflow, error)
	Create(ctx context.Context, wf *model.Workflow) error
	Update(ctx context.Context, wf *model.Workflow, expectedVersion int) error
	SetEnabled(ctx context.Context, id uuid.UUID, enabled bool, expectedVersion int, updatedBy string) (int, time.Time, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Audit(ctx context.Context, username, action string, detail map[string]any) error
	Ping(ctx context.Context) error
}

type API struct {
	Store    WorkflowStore
	Registry *components.Registry
	Engine   *engine.Engine
	Token    string
}

func New(st WorkflowStore, reg *components.Registry, token string) *API {
	return &API{Store: st, Registry: reg, Token: token}
}

type ctxKey int

const (
	userCtxKey ctxKey = iota
	roleCtxKey
)

func CurrentUser(r *http.Request) string {
	v, _ := r.Context().Value(userCtxKey).(string)
	return v
}

func CurrentRole(r *http.Request) string {
	v, _ := r.Context().Value(roleCtxKey).(string)
	return v
}

func (a *API) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", a.ready)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(a.ProxyAuth)
		r.Get("/workflow-components", a.catalog)

		r.Get("/workflows", a.listWorkflows)
		r.Post("/workflows", a.requireAdmin(a.createWorkflow))
		r.Post("/workflows/test-match", a.testMatch)
		r.Post("/workflows/preview", a.previewWorkflow)
		r.Get("/workflows/{id}", a.getWorkflow)
		r.Put("/workflows/{id}", a.requireAdmin(a.updateWorkflow))
		r.Delete("/workflows/{id}", a.requireAdmin(a.deleteWorkflow))
		r.Post("/workflows/{id}/duplicate", a.requireAdmin(a.duplicateWorkflow))
		r.Post("/workflows/{id}/enable", a.requireAdmin(a.enableWorkflow))
		r.Post("/workflows/{id}/disable", a.requireAdmin(a.disableWorkflow))
		r.Post("/workflows/{id}/test-match", a.testMatchByID)
		r.Post("/workflows/{id}/run", a.requireAdmin(a.runWorkflow))
		r.Get("/workflows/{id}/runs", a.listRuns)
		r.Get("/workflows/{id}/runs/{runId}", a.getRun)
		r.Post("/workflows/{id}/runs/{runId}/retry", a.requireAdmin(a.retryRun))
		r.Post("/workflows/{id}/runs/{runId}/cancel", a.requireAdmin(a.cancelRun))
	})
	return r
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "database unavailable", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// ProxyAuth trusts only the file-server proxy. Direct callers must present the
// same service token and identity headers the proxy injects.
func (a *API) ProxyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Token == "" {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "service token not configured", nil)
			return
		}
		got := r.Header.Get("X-Workflow-Proxy-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.Token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized", nil)
			return
		}
		user := r.Header.Get("X-Authenticated-User")
		if user == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated user", nil)
			return
		}
		role := r.Header.Get("X-Authenticated-Role")
		if role == "" {
			role = "readonly"
		}
		ctx := context.WithValue(r.Context(), userCtxKey, user)
		ctx = context.WithValue(ctx, roleCtxKey, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *API) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if CurrentRole(r) != "admin" {
			writeError(w, http.StatusForbidden, "forbidden", "admin role required", nil)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error  string            `json:"error"`
	Code   string            `json:"code"`
	Fields map[string]string `json:"fields,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, msg string, fields map[string]string) {
	writeJSON(w, status, errorBody{Error: msg, Code: code, Fields: fields})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body", nil)
		return false
	}
	return true
}

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "workflow not found", nil)
		return uuid.Nil, false
	}
	return id, true
}

func writeStoreFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "workflow not found", nil)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "workflow changed; reload and retry", nil)
	default:
		writeError(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}
