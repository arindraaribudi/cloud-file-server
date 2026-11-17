package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/action"
	"github.com/example/cos-workflow/internal/model"
)

// maxParallelSteps bounds how many steps of one parallel group run at once.
const maxParallelSteps = 8

func (e *Engine) executeOne(ctx context.Context, workerID string) error {
	run, err := e.Store.ClaimRun(ctx, workerID, e.Config.RunTimeout)
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}
	if run.Attempt > run.MaxAttempts {
		_ = e.Store.SkipPendingComponents(ctx, run.ID)
		_ = e.Store.FinishRun(ctx, run.ID, model.RunStatusFailed, "maximum attempts exceeded")
		return nil
	}
	if run.CancelRequested {
		_ = e.Store.SkipPendingComponents(ctx, run.ID)
		_ = e.Store.FinishRun(ctx, run.ID, model.RunStatusCanceled, "canceled")
		return nil
	}

	wf, err := e.Store.Get(ctx, run.WorkflowID)
	if err != nil {
		_ = e.Store.FinishRun(ctx, run.ID, model.RunStatusFailed, "workflow not found")
		return nil
	}
	comps, err := e.Store.LoadRunComponents(ctx, run.ID)
	if err != nil {
		_ = e.Store.FinishRun(ctx, run.ID, model.RunStatusFailed, "could not load run components")
		return nil
	}
	var rc model.RunContext
	if len(run.Context) > 0 {
		if err := json.Unmarshal(run.Context, &rc); err != nil {
			_ = e.Store.FinishRun(ctx, run.ID, model.RunStatusFailed, "invalid run context")
			return nil
		}
	}
	if rc.RunID == uuid.Nil {
		rc.RunID = run.ID
	}
	if rc.WorkflowID == uuid.Nil {
		rc.WorkflowID = run.WorkflowID
	}

	for _, batch := range batchComponents(comps) {
		pending := make([]*model.RunComponent, 0, len(batch))
		for _, c := range batch {
			if c.Status == model.ComponentStatusSucceeded || c.Status == model.ComponentStatusSkipped {
				continue
			}
			pending = append(pending, c)
		}
		if len(pending) == 0 {
			continue
		}
		canceled, err := e.Store.CancelRequested(ctx, run.ID)
		if err != nil {
			return err
		}
		if canceled {
			_ = e.Store.SkipPendingComponents(ctx, run.ID)
			_ = e.Store.FinishRun(ctx, run.ID, model.RunStatusCanceled, "canceled")
			return nil
		}
		for _, c := range pending {
			if _, ok := e.Registry.Get(c.TypeKey, c.TypeVersion); !ok {
				return e.failRun(ctx, run, c, fmt.Errorf("component %s v%d is not registered", c.TypeKey, c.TypeVersion))
			}
		}

		var done bool
		if len(pending) == 1 {
			var routeStopped bool
			done, routeStopped, err = e.runStep(ctx, run, wf, &rc, pending[0])
			if routeStopped {
				// keep the in-memory view in step with what was just persisted
				for i := range comps {
					if comps[i].Route == pending[0].Route && comps[i].Status == model.ComponentStatusPending {
						comps[i].Status = model.ComponentStatusSkipped
					}
				}
			}
		} else {
			done, err = e.runParallel(ctx, run, wf, rc, pending)
		}
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if err := e.Store.RenewLock(ctx, run.ID, time.Now().Add(e.Config.RunTimeout)); err != nil {
			e.Logger.Warn("workflow lease renewal failed", "run_id", run.ID, "err", err)
		}
	}
	if err := e.Store.FinishRun(ctx, run.ID, model.RunStatusSucceeded, ""); err != nil {
		return err
	}
	return nil
}

// batchComponents splits the executable (process/output) components into
// batches. Adjacent output components of the same route with the same non-empty
// ParallelGroup form one batch that runs concurrently; every other component is
// its own batch.
func batchComponents(comps []model.RunComponent) [][]*model.RunComponent {
	var out [][]*model.RunComponent
	for i := range comps {
		c := &comps[i]
		if c.Stage != model.StageProcess && c.Stage != model.StageOutput {
			continue
		}
		if n := len(out); n > 0 && c.Stage == model.StageOutput && c.ParallelGroup != "" {
			last := out[n-1]
			if last[0].Stage == model.StageOutput && last[0].ParallelGroup == c.ParallelGroup && last[0].Route == c.Route {
				out[n-1] = append(last, c)
				continue
			}
		}
		out = append(out, []*model.RunComponent{c})
	}
	return out
}

// runStep executes one component on its own. done reports that the run has
// been finished or requeued and the caller must stop. routeStopped reports that
// a condition was not met and the rest of its route has been skipped; the run
// carries on with its other routes.
func (e *Engine) runStep(ctx context.Context, run *model.Run, wf *model.Workflow, rc *model.RunContext, c *model.RunComponent) (done, routeStopped bool, err error) {
	if err := e.Store.StartComponent(ctx, c.ID, c.Attempt+1); err != nil {
		return false, false, err
	}
	stepCtx, cancel := context.WithTimeout(ctx, e.Config.ComponentTimeout)
	req := &action.Request{Workflow: wf, Run: run, Context: rc, Component: c}
	res, actionErr := e.Actions.Execute(stepCtx, req, run.Source == "test")
	cancel()
	if actionErr != nil {
		return true, false, e.handleFailure(ctx, run, c, actionErr)
	}
	out, _ := json.Marshal(res.Output)
	if err := e.Store.FinishComponent(ctx, c.ID, model.ComponentStatusSucceeded, out, ""); err != nil {
		return false, false, err
	}
	if res.Stop {
		if err := e.Store.SkipPendingRoute(ctx, run.ID, c.Route); err != nil {
			return false, false, err
		}
		e.Logger.Info("route skipped by condition", "run_id", run.ID, "route", c.Route, "component", c.TypeKey)
		return false, true, nil
	}
	if res.Path != "" && res.Path != rc.Path {
		rc.Path = res.Path
		ctxJSON, _ := json.Marshal(rc)
		if err := e.Store.UpdateRunContext(ctx, run.ID, ctxJSON); err != nil {
			return false, false, err
		}
	}
	return false, false, nil
}

type stepResult struct {
	res      action.Result
	err      error
	startErr error
}

// runParallel runs a batch of output components at the same time and waits for
// all of them. One failing step does not stop the others. Afterwards the run is
// retried (only the failed steps run again) when every failure is retryable,
// and failed otherwise.
func (e *Engine) runParallel(ctx context.Context, run *model.Run, wf *model.Workflow, rc model.RunContext, batch []*model.RunComponent) (done bool, err error) {
	results := make([]stepResult, len(batch))
	sem := make(chan struct{}, maxParallelSteps)
	var wg sync.WaitGroup
	for i, c := range batch {
		wg.Add(1)
		go func(i int, c *model.RunComponent) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := e.Store.StartComponent(ctx, c.ID, c.Attempt+1); err != nil {
				results[i].startErr = err
				return
			}
			local := rc // each step gets its own copy of the run context
			stepCtx, cancel := context.WithTimeout(ctx, e.Config.ComponentTimeout)
			defer cancel()
			req := &action.Request{Workflow: wf, Run: run, Context: &local, Component: c}
			results[i].res, results[i].err = e.Actions.Execute(stepCtx, req, run.Source == "test")
		}(i, c)
	}
	wg.Wait()

	var failures []string
	retryable := true
	for i, c := range batch {
		r := results[i]
		if r.startErr != nil {
			return false, r.startErr
		}
		if r.err != nil {
			out, _ := json.Marshal(map[string]any{"error": r.err.Error()})
			if err := e.Store.FinishComponent(ctx, c.ID, model.ComponentStatusFailed, out, r.err.Error()); err != nil {
				return false, err
			}
			label := c.Label
			if label == "" {
				label = c.TypeKey
			}
			failures = append(failures, label+": "+r.err.Error())
			if !(action.IsRetryable(r.err) || errors.Is(r.err, context.DeadlineExceeded)) {
				retryable = false
			}
			continue
		}
		out, _ := json.Marshal(r.res.Output)
		if err := e.Store.FinishComponent(ctx, c.ID, model.ComponentStatusSucceeded, out, ""); err != nil {
			return false, err
		}
	}
	if len(failures) == 0 {
		return false, nil
	}

	msg := strings.Join(failures, "; ")
	if retryable && run.Attempt < run.MaxAttempts {
		next := time.Now().Add(backoff(run.Attempt))
		if err := e.Store.RequeueRun(ctx, run.ID, msg, next); err != nil {
			return false, err
		}
		e.Logger.Warn("workflow run requeued", "run_id", run.ID, "attempt", run.Attempt, "next", next, "failed_steps", len(failures))
		return true, nil
	}
	_ = e.Store.SkipPendingComponents(ctx, run.ID)
	if err := e.Store.FinishRun(ctx, run.ID, model.RunStatusFailed, msg); err != nil {
		return false, err
	}
	e.Logger.Warn("workflow run failed", "run_id", run.ID, "failed_steps", len(failures), "err", msg)
	return true, nil
}

func (e *Engine) handleFailure(ctx context.Context, run *model.Run, c *model.RunComponent, actionErr error) error {
	out, _ := json.Marshal(map[string]any{"error": actionErr.Error()})
	_ = e.Store.FinishComponent(ctx, c.ID, model.ComponentStatusFailed, out, actionErr.Error())
	if (action.IsRetryable(actionErr) || errors.Is(actionErr, context.DeadlineExceeded)) && run.Attempt < run.MaxAttempts {
		next := time.Now().Add(backoff(run.Attempt))
		if err := e.Store.RequeueRun(ctx, run.ID, actionErr.Error(), next); err != nil {
			return err
		}
		e.Logger.Warn("workflow run requeued", "run_id", run.ID, "attempt", run.Attempt, "next", next, "err", actionErr)
		return nil
	}
	_ = e.Store.SkipPendingComponents(ctx, run.ID)
	if err := e.Store.FinishRun(ctx, run.ID, model.RunStatusFailed, actionErr.Error()); err != nil {
		return err
	}
	e.Logger.Warn("workflow run failed", "run_id", run.ID, "component", c.TypeKey, "err", actionErr)
	return nil
}

func (e *Engine) failRun(ctx context.Context, run *model.Run, c *model.RunComponent, err error) error {
	out, _ := json.Marshal(map[string]any{"error": err.Error()})
	_ = e.Store.FinishComponent(ctx, c.ID, model.ComponentStatusFailed, out, err.Error())
	_ = e.Store.SkipPendingComponents(ctx, run.ID)
	_ = e.Store.FinishRun(ctx, run.ID, model.RunStatusFailed, err.Error())
	return nil
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(attempt*attempt) * 10 * time.Second
}
