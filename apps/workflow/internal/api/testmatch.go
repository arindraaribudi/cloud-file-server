package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/engine"
	"github.com/example/cos-workflow/internal/model"
)

type testSample struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	Path      string `json:"path"`
	MIME      string `json:"mime"`
	EventType string `json:"event_type"`
	Size      int64  `json:"size"`
	IsDir     bool   `json:"is_dir"`
}

type testMatchInput struct {
	Sample      testSample       `json:"sample"`
	TriggerMode string           `json:"trigger_mode"`
	Components  []ComponentInput `json:"components"`
}

type triggerResult struct {
	ComponentID string `json:"component_id,omitempty"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Matched     bool   `json:"matched"`
	Reason      string `json:"reason"`
}

type workflowMatch struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Matched  bool            `json:"matched"`
	Triggers []triggerResult `json:"triggers"`
}

type testMatchResponse struct {
	Matched   bool            `json:"matched"`
	Triggers  []triggerResult `json:"triggers"`
	Workflows []workflowMatch `json:"workflows,omitempty"`
}

func (a *API) testMatch(w http.ResponseWriter, r *http.Request) {
	var in testMatchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Sample.EventType == "" {
		in.Sample.EventType = "upload"
	}
	if len(in.Components) == 0 {
		enabled := true
		list, err := a.Store.List(r.Context(), "", &enabled, 200, 0)
		if err != nil {
			writeStoreFailure(w, err)
			return
		}
		resp := testMatchResponse{Triggers: []triggerResult{}, Workflows: []workflowMatch{}}
		for _, wf := range list {
			matched, results := a.evaluateTriggers(wf, in.Sample, in.TriggerMode)
			if matched {
				resp.Matched = true
			}
			resp.Workflows = append(resp.Workflows, workflowMatch{
				ID: wf.ID.String(), Name: wf.Name, Matched: matched, Triggers: results,
			})
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	comps, err := a.parseTriggerComponents(in.Components)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	wf := &model.Workflow{TriggerMode: in.TriggerMode, Components: comps}
	matched, results := a.evaluateTriggers(wf, in.Sample, in.TriggerMode)
	writeJSON(w, http.StatusOK, testMatchResponse{Matched: matched, Triggers: results})
}

type previewInput struct {
	Sample     testSample       `json:"sample"`
	Components []ComponentInput `json:"components"`
}

type previewResponse struct {
	Matched  bool                 `json:"matched"`
	Triggers []triggerResult      `json:"triggers"`
	Steps    []engine.PreviewStep `json:"steps"`
}

// previewWorkflow shows what the given (possibly unsaved) workflow would do for
// a sample file: whether the trigger matches and what each step would change.
// It is a pure dry run: nothing is stored, executed or sent.
func (a *API) previewWorkflow(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "workflow engine is not configured", nil)
		return
	}
	var in previewInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Sample.EventType == "" {
		in.Sample.EventType = "upload"
	}
	comps, err := a.parsePreviewComponents(in.Components)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	wf := &model.Workflow{TriggerMode: model.TriggerModeAny, Components: comps}
	matched, results := a.evaluateTriggers(wf, in.Sample, model.TriggerModeAny)
	ev := model.FileEvent{
		UserID: in.Sample.UserID, Username: in.Sample.Username,
		Path: strings.TrimPrefix(in.Sample.Path, "/"), MIME: in.Sample.MIME,
		EventType: in.Sample.EventType, Size: in.Sample.Size, IsDir: in.Sample.IsDir,
	}
	writeJSON(w, http.StatusOK, previewResponse{
		Matched: matched, Triggers: results,
		Steps: a.Engine.Preview(r.Context(), wf, ev),
	})
}

// parsePreviewComponents resolves component types without validating the whole
// workflow, so an unfinished step shows its own error in the preview instead of
// failing the request.
func (a *API) parsePreviewComponents(in []ComponentInput) ([]model.Component, error) {
	out := make([]model.Component, 0, len(in))
	for i, ci := range in {
		stage := model.Stage(ci.Stage)
		if stage != model.StageTrigger && stage != model.StageProcess && stage != model.StageOutput {
			return nil, &components.ValidationError{Fields: map[string]string{
				fmt.Sprintf("components[%d].stage", i): "must be trigger, process, or output",
			}}
		}
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
		out = append(out, model.Component{
			Label: ci.Label, Stage: stage, TypeKey: t.Key, TypeVersion: t.Version,
			Config: cfg, Enabled: enabled, ParallelGroup: strings.TrimSpace(ci.ParallelGroup),
			Route: strings.TrimSpace(ci.Route),
		})
	}
	assignPositions(out)
	return out, nil
}

func (a *API) testMatchByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	wf, err := a.Store.Get(r.Context(), id)
	if err != nil {
		writeStoreFailure(w, err)
		return
	}
	var in testMatchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Sample.EventType == "" {
		in.Sample.EventType = "upload"
	}
	matched, results := a.evaluateTriggers(wf, in.Sample, in.TriggerMode)
	writeJSON(w, http.StatusOK, testMatchResponse{Matched: matched, Triggers: results})
}

func (a *API) parseTriggerComponents(in []ComponentInput) ([]model.Component, error) {
	out := make([]model.Component, 0, len(in))
	for i, ci := range in {
		if model.Stage(ci.Stage) != model.StageTrigger {
			return nil, &components.ValidationError{Fields: map[string]string{
				fmt.Sprintf("components[%d].stage", i): "must be trigger",
			}}
		}
		t, ok := a.Registry.Current(ci.Type)
		if !ok {
			return nil, &components.ValidationError{Fields: map[string]string{
				fmt.Sprintf("components[%d].type", i): "unknown component type " + ci.Type,
			}}
		}
		allowed := false
		for _, s := range t.Stages {
			if s == model.StageTrigger {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, &components.ValidationError{Fields: map[string]string{
				fmt.Sprintf("components[%d].stage", i): "type cannot be a trigger",
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
		out = append(out, model.Component{
			ID:          uuid.Nil,
			Label:       ci.Label,
			Stage:       model.StageTrigger,
			TypeKey:     t.Key,
			TypeVersion: t.Version,
			Position:    i,
			Config:      cfg,
			Enabled:     enabled,
		})
	}
	return out, nil
}

func (a *API) evaluateTriggers(wf *model.Workflow, sample testSample, mode string) (bool, []triggerResult) {
	if mode == "" {
		mode = wf.TriggerMode
	}
	if mode == "" {
		mode = model.TriggerModeAny
	}
	ev := model.FileEvent{
		UserID: sample.UserID, Username: sample.Username, Path: strings.TrimPrefix(sample.Path, "/"),
		MIME: sample.MIME, EventType: sample.EventType, Size: sample.Size, IsDir: sample.IsDir,
	}
	results := []triggerResult{}
	enabled := 0
	matched := 0
	for i := range wf.Components {
		c := wf.Components[i]
		if c.Stage != model.StageTrigger || !c.Enabled {
			continue
		}
		enabled++
		t, ok := a.Registry.Get(c.TypeKey, c.TypeVersion)
		if !ok {
			results = append(results, triggerResult{
				ComponentID: componentID(c.ID), Label: c.Label, Type: c.TypeKey,
				Matched: false, Reason: "unknown component type/version",
			})
			continue
		}
		res, err := components.MatchTrigger(t, c.Config, ev)
		if err != nil {
			results = append(results, triggerResult{
				ComponentID: componentID(c.ID), Label: c.Label, Type: c.TypeKey,
				Matched: false, Reason: err.Error(),
			})
			continue
		}
		if res.Matched {
			matched++
		}
		results = append(results, triggerResult{
			ComponentID: componentID(c.ID), Label: c.Label, Type: c.TypeKey,
			Matched: res.Matched, Reason: res.Reason,
		})
	}
	if mode == model.TriggerModeAll {
		return enabled > 0 && matched == enabled, results
	}
	return matched > 0, results
}

func componentID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
