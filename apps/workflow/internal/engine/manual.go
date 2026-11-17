package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

// RunManual queues a manual or dry-run execution without trigger evaluation.
func (e *Engine) RunManual(ctx context.Context, wf *model.Workflow, sample model.FileEvent, source string) (*model.Run, error) {
	if wf == nil {
		return nil, fmt.Errorf("workflow is required")
	}
	if source != "manual" && source != "test" {
		source = "manual"
	}
	if err := components.ValidateWorkflow(wf, e.Registry); err != nil {
		return nil, err
	}
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
	comps := make([]model.RunComponent, 0, len(wf.Components))
	for i := range wf.Components {
		c := wf.Components[i]
		cid := c.ID
		row := model.RunComponent{
			ComponentID: &cid, Stage: c.Stage, Label: c.Label, TypeKey: c.TypeKey,
			TypeVersion: c.TypeVersion, Position: c.Position, Config: c.Config,
			ParallelGroup: c.ParallelGroup,
			Route:         c.Route,
		}
		if c.Stage == model.StageTrigger {
			row.Status = model.ComponentStatusSkipped
			row.Output = matchOutput(false, "manual run")
		} else if c.Enabled {
			row.Status = model.ComponentStatusPending
		} else {
			row.Status = model.ComponentStatusSkipped
			row.Output = matchOutput(false, "component disabled")
		}
		comps = append(comps, row)
	}
	eventJSON, err := json.Marshal(sample)
	if err != nil {
		return nil, err
	}
	ctxJSON, err := json.Marshal(rc)
	if err != nil {
		return nil, err
	}
	run := &model.Run{
		ID: runID, WorkflowID: wf.ID, WorkflowVersion: wf.Version, TriggerMode: wf.TriggerMode,
		EventID: nil, Source: source, Status: model.RunStatusQueued,
		Event: eventJSON, Context: ctxJSON, MaxAttempts: e.Config.MaxAttempts,
	}
	tx, err := e.Store.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inserted, err := e.Store.InsertRunTx(ctx, tx, run, comps)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return nil, fmt.Errorf("could not queue manual run")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return run, nil
}

func (e *Engine) ListRuns(ctx context.Context, workflowID uuid.UUID, limit, offset int) ([]*model.Run, error) {
	return e.Store.ListRuns(ctx, workflowID, limit, offset)
}

func (e *Engine) GetRun(ctx context.Context, workflowID, runID uuid.UUID) (*model.Run, []model.RunComponent, error) {
	return e.Store.GetRun(ctx, workflowID, runID)
}

func (e *Engine) RetryRun(ctx context.Context, workflowID, runID uuid.UUID) error {
	return e.Store.RetryRun(ctx, workflowID, runID)
}

func (e *Engine) CancelRun(ctx context.Context, workflowID, runID uuid.UUID) error {
	return e.Store.CancelRun(ctx, workflowID, runID)
}
