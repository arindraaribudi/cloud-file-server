package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/action"
	"github.com/example/cos-workflow/internal/model"
)

// PreviewStep is what one process/output step would do for a sample file.
type PreviewStep struct {
	Label         string `json:"label"`
	Stage         string `json:"stage"`
	Type          string `json:"type"`
	ParallelGroup string `json:"parallel_group,omitempty"`
	Route         string `json:"route,omitempty"`
	// Status is "ok" (would run), "skipped" or "failed" (would fail).
	Status string         `json:"status"`
	Reason string         `json:"reason,omitempty"`
	Output map[string]any `json:"output,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// Preview evaluates the workflow's process and output steps in dry-run mode
// for a sample event. Nothing is persisted, no file is touched and no
// notification is sent. Like a real run, a condition that is not met skips the
// remaining steps, and a rename changes the path that later steps see.
func (e *Engine) Preview(ctx context.Context, wf *model.Workflow, sample model.FileEvent) []PreviewStep {
	if sample.EventTime.IsZero() {
		sample.EventTime = time.Now().UTC()
	}
	runID := uuid.New()
	rc := model.RunContext{
		RunID: runID, WorkflowID: wf.ID, Username: sample.Username, UserID: sample.UserID,
		RootFolder: sample.RootFolder, Path: sample.Path, OriginalPath: sample.Path,
		EventType: sample.EventType, EventTime: sample.EventTime, MIME: sample.MIME,
		Size: sample.Size, IsDir: sample.IsDir,
	}
	eventJSON, _ := json.Marshal(sample)
	run := &model.Run{ID: runID, WorkflowID: wf.ID, Source: "test", Status: model.RunStatusRunning, Event: eventJSON}

	steps := []PreviewStep{}
	// a condition that is not met skips the rest of its own route only
	stoppedRoutes := map[string]bool{}
	for _, stage := range []model.Stage{model.StageProcess, model.StageOutput} {
		for i := range wf.Components {
			c := wf.Components[i]
			if c.Stage != stage {
				continue
			}
			step := PreviewStep{Label: c.Label, Stage: string(c.Stage), Type: c.TypeKey, ParallelGroup: c.ParallelGroup, Route: c.Route}
			switch {
			case !c.Enabled:
				step.Status, step.Reason = "skipped", "this step is disabled"
			case stoppedRoutes[c.Route]:
				step.Status, step.Reason = "skipped", "the condition for this route was not met"
			default:
				stepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				req := &action.Request{
					Workflow: wf, Run: run, Context: &rc,
					Component: &model.RunComponent{
						Stage: c.Stage, Label: c.Label, TypeKey: c.TypeKey,
						TypeVersion: c.TypeVersion, Position: c.Position, Config: c.Config,
					},
				}
				res, err := e.Actions.Execute(stepCtx, req, true)
				cancel()
				if err != nil {
					step.Status, step.Error = "failed", err.Error()
					break
				}
				step.Status, step.Output = "ok", res.Output
				if res.Stop {
					stoppedRoutes[c.Route] = true
				}
				if res.Path != "" {
					rc.Path = res.Path
				}
			}
			steps = append(steps, step)
		}
	}
	return steps
}
