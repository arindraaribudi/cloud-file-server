package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

type WorkflowInput struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Enabled     *bool            `json:"enabled"`
	Version     int              `json:"version"`
	TriggerMode string           `json:"trigger_mode"`
	Components  []ComponentInput `json:"components"`
}

type ComponentInput struct {
	Label       string          `json:"label"`
	Stage       string          `json:"stage"`
	Type        string          `json:"type"`
	TypeVersion int             `json:"type_version,omitempty"`
	Config      json.RawMessage `json:"config"`
	Enabled     *bool           `json:"enabled"`
	// ParallelGroup makes adjacent output components with the same value run
	// at the same time. Empty means one after another.
	ParallelGroup string `json:"parallel_group,omitempty"`
	// Route ties a step to one Condition (see model.Component.Route).
	Route string `json:"route,omitempty"`
}

func (a *API) listWorkflows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	var enabled *bool
	if v := q.Get("enabled"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "enabled must be true or false", nil)
			return
		}
		enabled = &b
	}
	list, err := a.Store.List(r.Context(), strings.TrimSpace(q.Get("search")), enabled, limit, offset)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) getWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	wf, err := a.Store.Get(r.Context(), id)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

func (a *API) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var in WorkflowInput
	if !decodeJSON(w, r, &in) {
		return
	}
	wf, err := a.buildWorkflow(in, nil)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	wf.CreatedBy = CurrentUser(r)
	wf.UpdatedBy = CurrentUser(r)
	if err := a.Store.Create(r.Context(), wf); err != nil {
		writeStoreFailure(w, err)
		return
	}
	a.auditWorkflow(r, "ADMIN_WORKFLOW_CREATE", wf, nil)
	writeJSON(w, http.StatusCreated, wf)
}

func (a *API) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	existing, err := a.Store.Get(r.Context(), id)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	var in WorkflowInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Version <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "version is required", nil)
		return
	}
	wf, err := a.buildWorkflow(in, existing)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	wf.ID = id
	wf.Version = in.Version
	wf.CreatedAt = existing.CreatedAt
	wf.CreatedBy = existing.CreatedBy
	wf.UpdatedBy = CurrentUser(r)
	if err := a.Store.Update(r.Context(), wf, in.Version); err != nil {
		writeStoreFailure(w, err)
		return
	}
	a.auditWorkflow(r, "ADMIN_WORKFLOW_UPDATE", wf, nil)
	writeJSON(w, http.StatusOK, wf)
}

func (a *API) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	wf, err := a.Store.Get(r.Context(), id)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	if err := a.Store.Delete(r.Context(), id); err != nil {
		writeStoreFailure(w, err)
		return
	}
	a.auditWorkflow(r, "ADMIN_WORKFLOW_DELETE", wf, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) enableWorkflow(w http.ResponseWriter, r *http.Request) {
	a.setEnabled(w, r, true)
}

func (a *API) disableWorkflow(w http.ResponseWriter, r *http.Request) {
	a.setEnabled(w, r, false)
}

func (a *API) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req struct {
		Version int `json:"version"`
	}
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body", nil)
		return
	}
	wf, err := a.Store.Get(r.Context(), id)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	version := req.Version
	if version <= 0 {
		version = wf.Version
	}
	wf.Enabled = enabled
	if enabled {
		if err := components.ValidateWorkflow(wf, a.Registry); err != nil {
			writeWorkflowError(w, err)
			return
		}
	}
	newVersion, updatedAt, err := a.Store.SetEnabled(r.Context(), id, enabled, version, CurrentUser(r))
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	wf.Version = newVersion
	a.auditWorkflow(r, "ADMIN_WORKFLOW_ENABLE", wf, map[string]any{"enabled": enabled})
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": enabled, "version": newVersion, "updated_at": updatedAt,
	})
}

func (a *API) duplicateWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body", nil)
		return
	}
	src, err := a.Store.Get(r.Context(), id)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = src.Name + " (copy)"
	}
	cp := *src
	cp.ID = uuid.Nil
	cp.Name = name
	cp.Enabled = false
	cp.Version = 0
	cp.CreatedBy = CurrentUser(r)
	cp.UpdatedBy = CurrentUser(r)
	cp.Components = append([]model.Component(nil), src.Components...)
	for i := range cp.Components {
		cp.Components[i].ID = uuid.Nil
	}
	if err := a.Store.Create(r.Context(), &cp); err != nil {
		writeStoreFailure(w, err)
		return
	}
	a.auditWorkflow(r, "ADMIN_WORKFLOW_DUPLICATE", &cp, map[string]any{"source_workflow_id": src.ID.String()})
	writeJSON(w, http.StatusCreated, cp)
}

func (a *API) buildWorkflow(in WorkflowInput, existing *model.Workflow) (*model.Workflow, error) {
	wf := &model.Workflow{
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		TriggerMode: strings.TrimSpace(in.TriggerMode),
		Version:     in.Version,
	}
	if wf.TriggerMode == "" {
		wf.TriggerMode = model.TriggerModeAny
	}
	if in.Enabled != nil {
		wf.Enabled = *in.Enabled
	} else if existing != nil {
		wf.Enabled = existing.Enabled
	}
	for i, ci := range in.Components {
		stage := model.Stage(ci.Stage)
		t, ok := a.Registry.Current(ci.Type)
		if !ok {
			return nil, &components.ValidationError{Fields: map[string]string{
				fmt.Sprintf("components[%d].type", i): "unknown component type " + ci.Type,
			}}
		}
		cfg := ci.Config
		if len(cfg) == 0 || string(cfg) == "null" {
			cfg = t.DefaultConfig
		}
		enabled := true
		if ci.Enabled != nil {
			enabled = *ci.Enabled
		}
		wf.Components = append(wf.Components, model.Component{
			Label:         ci.Label,
			Stage:         stage,
			TypeKey:       t.Key,
			TypeVersion:   t.Version,
			Config:        cfg,
			Enabled:       enabled,
			ParallelGroup: strings.TrimSpace(ci.ParallelGroup),
			Route:         strings.TrimSpace(ci.Route),
		})
	}
	assignPositions(wf.Components)
	if err := components.ValidateWorkflow(wf, a.Registry); err != nil {
		return nil, err
	}
	return wf, nil
}

func assignPositions(comps []model.Component) {
	counts := map[model.Stage]int{}
	for i := range comps {
		comps[i].Position = counts[comps[i].Stage]
		counts[comps[i].Stage]++
	}
}

func (a *API) auditWorkflow(r *http.Request, action string, wf *model.Workflow, extra map[string]any) {
	if wf == nil {
		return
	}
	detail := map[string]any{
		"workflow_id": wf.ID.String(),
		"name":        wf.Name,
		"version":     wf.Version,
	}
	for k, v := range extra {
		detail[k] = v
	}
	_ = a.Store.Audit(r.Context(), CurrentUser(r), action, detail)
}

func writeWorkflowError(w http.ResponseWriter, err error) {
	var ve *components.ValidationError
	if errors.As(err, &ve) {
		writeError(w, http.StatusBadRequest, "validation_failed", "invalid workflow", ve.Fields)
		return
	}
	writeError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
