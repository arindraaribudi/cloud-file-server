package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

func (e *Engine) dispatchBatch(ctx context.Context) (int, int, error) {
	workflows, err := e.Store.EnabledWorkflows(ctx)
	if err != nil {
		return 0, 0, err
	}
	valid := make([]*model.Workflow, 0, len(workflows))
	for _, wf := range workflows {
		if err := components.ValidateWorkflow(wf, e.Registry); err != nil {
			e.Logger.Warn("workflow is invalid and will be skipped", "workflow_id", wf.ID, "err", err)
			continue
		}
		valid = append(valid, wf)
	}

	tx, err := e.Store.Pool().Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, event_time, event_type, user_id, username, root_folder,
		       connection_type, backend_location, path, old_path, is_dir, size,
		       mime, mime_source, overwrite, session_id
		FROM file_events
		WHERE dispatched_at IS NULL
		ORDER BY id
		LIMIT 100
		FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return 0, 0, err
	}
	events := make([]model.FileEvent, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		var (
			ev      model.FileEvent
			userID  *int64
			oldPath *string
			session *uuid.UUID
		)
		if err := rows.Scan(&ev.ID, &ev.EventTime, &ev.EventType, &userID, &ev.Username,
			&ev.RootFolder, &ev.ConnectionType, &ev.BackendLocation, &ev.Path,
			&oldPath, &ev.IsDir, &ev.Size, &ev.MIME, &ev.MIMESource, &ev.Overwrite,
			&session); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if userID != nil {
			ev.UserID = *userID
		}
		if oldPath != nil {
			ev.OldPath = *oldPath
		}
		if session != nil {
			ev.SessionID = *session
		}
		events = append(events, ev)
		ids = append(ids, ev.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()

	runs := 0
	for i := range events {
		for _, wf := range valid {
			run, comps, matched, err := e.buildRun(wf, &events[i])
			if err != nil {
				return 0, 0, err
			}
			if !matched {
				continue
			}
			inserted, err := e.Store.InsertRunTx(ctx, tx, run, comps)
			if err != nil {
				return 0, 0, err
			}
			if inserted {
				runs++
			}
		}
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE file_events SET dispatched_at=now() WHERE id = ANY($1::bigint[])`, ids); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return len(events), runs, nil
}

func (e *Engine) buildRun(wf *model.Workflow, ev *model.FileEvent) (*model.Run, []model.RunComponent, bool, error) {
	runID := uuid.New()
	mode := wf.TriggerMode
	if mode == "" {
		mode = model.TriggerModeAny
	}
	rc := model.RunContext{
		RunID: runID, WorkflowID: wf.ID, Username: ev.Username, UserID: ev.UserID,
		RootFolder: ev.RootFolder, Path: ev.Path, OriginalPath: ev.Path,
		EventType: ev.EventType, EventTime: ev.EventTime, MIME: ev.MIME,
		Size: ev.Size, IsDir: ev.IsDir,
	}
	comps := make([]model.RunComponent, 0, len(wf.Components))
	enabledTriggers := 0
	matchedTriggers := 0

	for i := range wf.Components {
		c := wf.Components[i]
		if c.Stage != model.StageTrigger {
			continue
		}
		cid := c.ID
		row := model.RunComponent{
			ComponentID: &cid, Stage: c.Stage, Label: c.Label, TypeKey: c.TypeKey,
			TypeVersion: c.TypeVersion, Position: c.Position, Config: c.Config,
		}
		if !c.Enabled {
			row.Status = model.ComponentStatusSkipped
			row.Output = matchOutput(false, "component disabled")
			comps = append(comps, row)
			continue
		}
		enabledTriggers++
		t, ok := e.Registry.Get(c.TypeKey, c.TypeVersion)
		if !ok {
			return nil, nil, false, fmt.Errorf("component %s v%d is not registered", c.TypeKey, c.TypeVersion)
		}
		res, err := components.MatchTrigger(t, c.Config, *ev)
		if err != nil {
			return nil, nil, false, err
		}
		row.Status = model.ComponentStatusNotMatched
		if res.Matched {
			row.Status = model.ComponentStatusMatched
			matchedTriggers++
		}
		row.Output = matchOutput(res.Matched, res.Reason)
		comps = append(comps, row)
	}

	matched := matchedTriggers > 0
	if mode == model.TriggerModeAll {
		matched = enabledTriggers > 0 && matchedTriggers == enabledTriggers
	}
	if !matched {
		return nil, nil, false, nil
	}

	for i := range wf.Components {
		c := wf.Components[i]
		if c.Stage == model.StageTrigger {
			continue
		}
		cid := c.ID
		row := model.RunComponent{
			ComponentID: &cid, Stage: c.Stage, Label: c.Label, TypeKey: c.TypeKey,
			TypeVersion: c.TypeVersion, Position: c.Position, Config: c.Config,
			ParallelGroup: c.ParallelGroup,
			Route:         c.Route,
		}
		if c.Enabled {
			row.Status = model.ComponentStatusPending
		} else {
			row.Status = model.ComponentStatusSkipped
			row.Output = matchOutput(false, "component disabled")
		}
		comps = append(comps, row)
	}

	eventJSON, err := json.Marshal(ev)
	if err != nil {
		return nil, nil, false, err
	}
	ctxJSON, err := json.Marshal(rc)
	if err != nil {
		return nil, nil, false, err
	}
	run := &model.Run{
		ID: runID, WorkflowID: wf.ID, WorkflowVersion: wf.Version, TriggerMode: mode,
		EventID: &ev.ID, Source: "event", Status: model.RunStatusQueued,
		Event: eventJSON, Context: ctxJSON, MaxAttempts: e.Config.MaxAttempts,
	}
	return run, comps, true, nil
}

func matchOutput(matched bool, reason string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"matched": matched, "reason": reason})
	return b
}
