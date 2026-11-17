package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

type manualRunInput struct {
	Username  string      `json:"username"`
	Path      string      `json:"path"`
	MIME      string      `json:"mime"`
	EventType string      `json:"event_type"`
	Size      int64       `json:"size"`
	IsDir     bool        `json:"is_dir"`
	UserID    int64       `json:"user_id"`
	DryRun    bool        `json:"dry_run"`
	Sample    *testSample `json:"sample,omitempty"`
}

type runDetail struct {
	Run        *model.Run           `json:"run"`
	Components []model.RunComponent `json:"components"`
}

func (a *API) runWorkflow(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "workflow engine is not configured", nil)
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	wf, err := a.Store.Get(r.Context(), id)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	if err := components.ValidateWorkflow(wf, a.Registry); err != nil {
		writeWorkflowError(w, err)
		return
	}
	var in manualRunInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sample := testSample{
		Username: in.Username, Path: in.Path, MIME: in.MIME,
		EventType: in.EventType, Size: in.Size, IsDir: in.IsDir, UserID: in.UserID,
	}
	if in.Sample != nil {
		sample = *in.Sample
	}
	event, err := eventFromSample(sample)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	source := "manual"
	if in.DryRun {
		source = "test"
	}
	run, err := a.Engine.RunManual(r.Context(), wf, event, source)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	a.auditWorkflow(r, "ADMIN_WORKFLOW_RUN", wf, map[string]any{
		"run_id": run.ID.String(), "dry_run": in.DryRun,
	})
	writeJSON(w, http.StatusAccepted, run)
}

func (a *API) listRuns(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "workflow engine is not configured", nil)
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	runs, err := a.Engine.ListRuns(r.Context(), id, limit, offset)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (a *API) getRun(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "workflow engine is not configured", nil)
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	run, comps, err := a.Engine.GetRun(r.Context(), id, runID)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runDetail{Run: run, Components: comps})
}

func (a *API) retryRun(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "workflow engine is not configured", nil)
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	if err := a.Engine.RetryRun(r.Context(), id, runID); err != nil {
		writeStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": model.RunStatusQueued})
}

func (a *API) cancelRun(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "workflow engine is not configured", nil)
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	if err := a.Engine.CancelRun(r.Context(), id, runID); err != nil {
		writeStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancel_requested"})
}

func parseRunID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "runId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "run not found", nil)
		return uuid.Nil, false
	}
	return id, true
}

func eventFromSample(s testSample) (model.FileEvent, error) {
	if strings.TrimSpace(s.Username) == "" {
		return model.FileEvent{}, errString("username is required")
	}
	p := strings.TrimPrefix(strings.TrimSpace(s.Path), "/")
	if p == "" {
		return model.FileEvent{}, errString("path is required")
	}
	eventType := s.EventType
	if eventType == "" {
		eventType = "upload"
	}
	if eventType != "upload" && eventType != "remove" {
		return model.FileEvent{}, errString("event_type must be upload or remove")
	}
	return model.FileEvent{
		EventTime: time.Now().UTC(), EventType: eventType,
		UserID: s.UserID, Username: s.Username, Path: p,
		IsDir: s.IsDir, Size: s.Size, MIME: components.NormalizeMediaType(s.MIME),
	}, nil
}

type errString string

func (e errString) Error() string { return string(e) }
