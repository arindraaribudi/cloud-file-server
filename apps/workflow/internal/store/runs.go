package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/cos-workflow/internal/model"
)

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// EnabledWorkflows loads every enabled workflow with its components. The
// dispatcher evaluates all of them against each file event.
func (s *Store) EnabledWorkflows(ctx context.Context) ([]*model.Workflow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, enabled, version, trigger_mode,
		       created_by, updated_by, created_at, updated_at
		FROM workflow.workflows
		WHERE deleted_at IS NULL AND enabled = TRUE
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.Workflow, 0)
	for rows.Next() {
		wf, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		if err := s.loadComponents(ctx, wf); err != nil {
			return nil, err
		}
		out = append(out, wf)
	}
	return out, rows.Err()
}

// InsertRunTx inserts a run and its component snapshots. It returns false when
// the (workflow_id, event_id) pair already has a run.
func (s *Store) InsertRunTx(ctx context.Context, tx pgx.Tx, run *model.Run, comps []model.RunComponent) (bool, error) {
	if run.Status == "" {
		run.Status = model.RunStatusQueued
	}
	if run.MaxAttempts == 0 {
		run.MaxAttempts = 3
	}
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	event := run.Event
	if len(event) == 0 {
		event = json.RawMessage(`{}`)
	}
	runCtx := run.Context
	if len(runCtx) == 0 {
		runCtx = json.RawMessage(`{}`)
	}
	var eventID any
	if run.EventID != nil {
		eventID = *run.EventID
	}

	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO workflow.runs
		(id, workflow_id, workflow_version, trigger_mode, event_id, source, status,
		 event, context, attempt, max_attempts)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,0,$10)
		ON CONFLICT (workflow_id, event_id) DO NOTHING
		RETURNING created_at`,
		run.ID, run.WorkflowID, run.WorkflowVersion, run.TriggerMode, eventID, run.Source,
		run.Status, event, runCtx, run.MaxAttempts).
		Scan(&createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	run.CreatedAt = createdAt
	run.Context = runCtx

	for i := range comps {
		c := &comps[i]
		config := c.Config
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		output := c.Output
		if len(output) == 0 {
			output = json.RawMessage(`{}`)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO workflow.run_components
			(run_id, component_id, stage, label, type_key, type_version, position,
			 config, status, attempt, output, parallel_group, route)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11::jsonb,$12,$13)
			RETURNING id`,
			run.ID, c.ComponentID, c.Stage, c.Label, c.TypeKey, c.TypeVersion,
			c.Position, config, c.Status, c.Attempt, output, c.ParallelGroup, c.Route).
			Scan(&c.ID); err != nil {
			return false, err
		}
		c.RunID = run.ID
	}
	return true, nil
}

// ClaimRun selects one queued run, takes the per-workflow lock, and marks the
// run running. It returns (nil, nil) when no run is claimable.
func (s *Store) ClaimRun(ctx context.Context, workerID string, timeout time.Duration) (*model.Run, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	run, err := scanRun(tx.QueryRow(ctx, `
		SELECT r.id, r.workflow_id, r.workflow_version, r.trigger_mode, r.event_id,
		       r.source, r.status, r.event, r.context, r.error, r.attempt,
		       r.max_attempts, r.cancel_requested, r.next_attempt_at,
		       r.locked_by, r.locked_until, r.created_at, r.started_at, r.finished_at
		FROM workflow.runs r
		JOIN workflow.workflows w ON w.id = r.workflow_id
		WHERE r.status = 'queued'
		  AND (r.next_attempt_at IS NULL OR r.next_attempt_at <= now())
		  AND w.enabled = TRUE AND w.deleted_at IS NULL
		ORDER BY r.created_at
		LIMIT 1
		FOR UPDATE OF r SKIP LOCKED`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	lockedUntil := time.Now().Add(timeout)
	tag, err := tx.Exec(ctx, `
		INSERT INTO workflow.run_locks (workflow_id, run_id, locked_until)
		VALUES ($1,$2,$3)
		ON CONFLICT (workflow_id) DO NOTHING`, run.WorkflowID, run.ID, lockedUntil)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, nil
	}

	var attempt int
	var startedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE workflow.runs
		SET status='running', attempt=attempt+1,
		    started_at=COALESCE(started_at, now()),
		    locked_by=$2, locked_until=$3
		WHERE id=$1
		RETURNING attempt, started_at`, run.ID, workerID, lockedUntil).
		Scan(&attempt, &startedAt)
	if err != nil {
		return nil, err
	}
	run.Status = model.RunStatusRunning
	run.Attempt = attempt
	run.StartedAt = &startedAt
	run.LockedBy = workerID
	run.LockedUntil = &lockedUntil
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *Store) LoadRunComponents(ctx context.Context, runID uuid.UUID) ([]model.RunComponent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, component_id, stage, label, type_key, type_version,
		       position, config, status, attempt, started_at, finished_at, error, output,
		       parallel_group, route
		FROM workflow.run_components
		WHERE run_id=$1
		ORDER BY CASE stage WHEN 'process' THEN 0 WHEN 'output' THEN 1 ELSE 2 END, position`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.RunComponent, 0)
	for rows.Next() {
		var (
			c      model.RunComponent
			stage  string
			config []byte
			output []byte
		)
		if err := rows.Scan(&c.ID, &c.RunID, &c.ComponentID, &stage, &c.Label,
			&c.TypeKey, &c.TypeVersion, &c.Position, &config, &c.Status, &c.Attempt,
			&c.StartedAt, &c.FinishedAt, &c.Error, &output, &c.ParallelGroup, &c.Route); err != nil {
			return nil, err
		}
		c.Stage = model.Stage(stage)
		c.Config = json.RawMessage(config)
		c.Output = json.RawMessage(output)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) StartComponent(ctx context.Context, id uuid.UUID, attempt int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflow.run_components
		SET status='running', attempt=$2, started_at=now(), error=''
		WHERE id=$1`, id, attempt)
	return err
}

func (s *Store) FinishComponent(ctx context.Context, id uuid.UUID, status string, output json.RawMessage, errText string) error {
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE workflow.run_components
		SET status=$2, output=$3::jsonb, error=$4, finished_at=now()
		WHERE id=$1`, id, status, output, errText)
	return err
}

func (s *Store) UpdateRunContext(ctx context.Context, runID uuid.UUID, runCtx json.RawMessage) error {
	if len(runCtx) == 0 {
		runCtx = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `UPDATE workflow.runs SET context=$2::jsonb WHERE id=$1`, runID, runCtx)
	return err
}

func (s *Store) FinishRun(ctx context.Context, runID uuid.UUID, status, errText string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM workflow.run_locks WHERE run_id=$1`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow.runs
		SET status=$2, error=$3, finished_at=now(), locked_by=NULL, locked_until=NULL
		WHERE id=$1`, runID, status, errText); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RequeueRun(ctx context.Context, runID uuid.UUID, errText string, next time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM workflow.run_locks WHERE run_id=$1`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow.runs
		SET status='queued', error=$2, next_attempt_at=$3,
		    locked_by=NULL, locked_until=NULL, finished_at=NULL
		WHERE id=$1`, runID, errText, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RetryRun(ctx context.Context, workflowID, runID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE workflow.runs
		SET status='queued', error='', next_attempt_at=NULL, finished_at=NULL,
		    locked_by=NULL, locked_until=NULL, cancel_requested=FALSE, attempt=0
		WHERE id=$1 AND workflow_id=$2 AND status IN ('failed','canceled')`, runID, workflowID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow.runs WHERE id=$1 AND workflow_id=$2)`, runID, workflowID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workflow.run_locks WHERE run_id=$1`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow.run_components
		SET status='pending', attempt=0, started_at=NULL, finished_at=NULL,
		    error='', output='{}'::jsonb
		WHERE run_id=$1 AND stage IN ('process','output') AND status <> 'succeeded'`, runID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListRuns(ctx context.Context, workflowID uuid.UUID, limit, offset int) ([]*model.Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, workflow_id, workflow_version, trigger_mode, event_id, source,
		       status, event, context, error, attempt, max_attempts, cancel_requested,
		       next_attempt_at, locked_by, locked_until, created_at, started_at, finished_at
		FROM workflow.runs
		WHERE workflow_id=$1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, workflowID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.Run, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) GetRun(ctx context.Context, workflowID, runID uuid.UUID) (*model.Run, []model.RunComponent, error) {
	run, err := scanRun(s.pool.QueryRow(ctx, `
		SELECT id, workflow_id, workflow_version, trigger_mode, event_id, source,
		       status, event, context, error, attempt, max_attempts, cancel_requested,
		       next_attempt_at, locked_by, locked_until, created_at, started_at, finished_at
		FROM workflow.runs WHERE id=$1 AND workflow_id=$2`, runID, workflowID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	comps, err := s.LoadRunComponents(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	return run, comps, nil
}

func (s *Store) CancelRun(ctx context.Context, workflowID, runID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE workflow.runs SET cancel_requested=TRUE
		WHERE id=$1 AND workflow_id=$2 AND status IN ('queued','running')`, runID, workflowID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReapExpiredLocks requeues runs whose worker lease expired.
func (s *Store) ReapExpiredLocks(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		WITH expired AS (
			DELETE FROM workflow.run_locks
			WHERE locked_until < now()
			RETURNING run_id
		)
		UPDATE workflow.runs r
		SET status='queued', locked_by=NULL, locked_until=NULL, next_attempt_at=now()
		FROM expired e
		WHERE r.id=e.run_id AND r.status='running'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CancelDisabledRuns cancels queued runs whose workflow was disabled or
// soft-deleted before they started.
func (s *Store) CancelDisabledRuns(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE workflow.runs r
		SET status='canceled', finished_at=now(), error='workflow disabled or deleted'
		WHERE r.status='queued'
		  AND EXISTS (
			SELECT 1 FROM workflow.workflows w
			WHERE w.id=r.workflow_id AND (w.enabled=FALSE OR w.deleted_at IS NOT NULL)
		  )`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) CleanupRuns(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM workflow.runs
		WHERE status IN ('succeeded','failed','canceled')
		  AND finished_at IS NOT NULL AND finished_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) CancelRequested(ctx context.Context, runID uuid.UUID) (bool, error) {
	var v bool
	if err := s.pool.QueryRow(ctx, `SELECT cancel_requested FROM workflow.runs WHERE id=$1`, runID).Scan(&v); err != nil {
		return false, err
	}
	return v, nil
}

func (s *Store) SkipPendingComponents(ctx context.Context, runID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflow.run_components
		SET status='skipped', finished_at=now()
		WHERE run_id=$1 AND stage IN ('process','output')
		  AND status IN ('pending','running')`, runID)
	return err
}

// SkipPendingRoute skips the not-yet-run steps of one route, used when that
// route's condition is not met.
func (s *Store) SkipPendingRoute(ctx context.Context, runID uuid.UUID, route string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflow.run_components
		SET status='skipped', finished_at=now(),
		    output='{"reason":"the condition for this route was not met"}'::jsonb
		WHERE run_id=$1 AND route=$2 AND stage IN ('process','output')
		  AND status = 'pending'`, runID, route)
	return err
}

func (s *Store) RenewLock(ctx context.Context, runID uuid.UUID, until time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE workflow.run_locks SET locked_until=$2 WHERE run_id=$1`, runID, until); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow.runs SET locked_until=$2 WHERE id=$1`, runID, until); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanRun(row interface{ Scan(...any) error }) (*model.Run, error) {
	run := &model.Run{}
	var (
		event, runCtx []byte
		lockedBy      *string
	)
	if err := row.Scan(&run.ID, &run.WorkflowID, &run.WorkflowVersion, &run.TriggerMode,
		&run.EventID, &run.Source, &run.Status, &event, &runCtx, &run.Error,
		&run.Attempt, &run.MaxAttempts, &run.CancelRequested, &run.NextAttemptAt,
		&lockedBy, &run.LockedUntil, &run.CreatedAt, &run.StartedAt, &run.FinishedAt); err != nil {
		return nil, err
	}
	run.Event = json.RawMessage(event)
	run.Context = json.RawMessage(runCtx)
	if lockedBy != nil {
		run.LockedBy = *lockedBy
	}
	return run, nil
}
