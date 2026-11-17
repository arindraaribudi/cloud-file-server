package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/cos-workflow/internal/model"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("version or name conflict")
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Audit(ctx context.Context, username, action string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO public.audit_events
		(event_time, username, action, event_type, connection_type, path, bytes, success, detail)
		VALUES (now(), $1, $2, 'workflow', 'workflow', '', 0, TRUE, $3::jsonb)`,
		username, action, b)
	return err
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) List(ctx context.Context, search string, enabled *bool, limit, offset int) ([]*model.Workflow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var enabledArg any
	if enabled != nil {
		enabledArg = *enabled
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, enabled, version, trigger_mode,
		       created_by, updated_by, created_at, updated_at
		FROM workflow.workflows
		WHERE deleted_at IS NULL
		  AND ($1 = '' OR name ILIKE '%' || $1 || '%')
		  AND ($2::boolean IS NULL OR enabled = $2)
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`, search, enabledArg, limit, offset)
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

func (s *Store) Get(ctx context.Context, id uuid.UUID) (*model.Workflow, error) {
	wf, err := scanWorkflow(s.pool.QueryRow(ctx, `
		SELECT id, name, description, enabled, version, trigger_mode,
		       created_by, updated_by, created_at, updated_at
		FROM workflow.workflows WHERE id=$1 AND deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadComponents(ctx, wf); err != nil {
		return nil, err
	}
	return wf, nil
}

func (s *Store) Create(ctx context.Context, wf *model.Workflow) error {
	wf.Components = normalizePositions(wf.Components)
	if wf.TriggerMode == "" {
		wf.TriggerMode = model.TriggerModeAny
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var version int
	var createdAt, updatedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO workflow.workflows
		(name, description, enabled, trigger_mode, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, version, created_at, updated_at`,
		wf.Name, wf.Description, wf.Enabled, wf.TriggerMode, wf.CreatedBy, wf.UpdatedBy).
		Scan(&wf.ID, &version, &createdAt, &updatedAt)
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	wf.Version = version
	wf.CreatedAt = createdAt
	wf.UpdatedAt = updatedAt

	if err := insertComponents(ctx, tx, wf.ID, wf.Components); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Update(ctx context.Context, wf *model.Workflow, expectedVersion int) error {
	wf.Components = normalizePositions(wf.Components)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current int
	if err := tx.QueryRow(ctx, `SELECT version FROM workflow.workflows WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, wf.ID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if current != expectedVersion {
		return ErrConflict
	}
	var version int
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE workflow.workflows
		SET name=$2, description=$3, enabled=$4, trigger_mode=$5,
		    updated_by=$6, version=version+1, updated_at=now()
		WHERE id=$1
		RETURNING version, updated_at`,
		wf.ID, wf.Name, wf.Description, wf.Enabled, wf.TriggerMode, wf.UpdatedBy).
		Scan(&version, &updatedAt)
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workflow.components WHERE workflow_id=$1`, wf.ID); err != nil {
		return err
	}
	if err := insertComponents(ctx, tx, wf.ID, wf.Components); err != nil {
		return err
	}
	wf.Version = version
	wf.UpdatedAt = updatedAt
	return tx.Commit(ctx)
}

func (s *Store) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool, expectedVersion int, updatedBy string) (int, time.Time, error) {
	var version int
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
		UPDATE workflow.workflows
		SET enabled=$2, updated_by=$3, version=version+1, updated_at=now()
		WHERE id=$1 AND version=$4 AND deleted_at IS NULL
		RETURNING version, updated_at`, id, enabled, updatedBy, expectedVersion).
		Scan(&version, &updatedAt)
	if err == nil {
		return version, updatedAt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, err
	}
	var exists bool
	if qerr := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow.workflows WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists); qerr != nil {
		return 0, time.Time{}, qerr
	}
	if !exists {
		return 0, time.Time{}, ErrNotFound
	}
	return 0, time.Time{}, ErrConflict
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE workflow.workflows SET deleted_at=now(), enabled=FALSE WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) loadComponents(ctx context.Context, wf *model.Workflow) error {
	rows, err := s.pool.Query(ctx, `
		SELECT id, label, stage, type_key, type_version, position, config, enabled, parallel_group, route
		FROM workflow.components
		WHERE workflow_id=$1
		ORDER BY CASE stage WHEN 'trigger' THEN 0 WHEN 'process' THEN 1 ELSE 2 END, position`, wf.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	wf.Components = make([]model.Component, 0)
	for rows.Next() {
		var (
			c     model.Component
			stage string
			raw   []byte
		)
		if err := rows.Scan(&c.ID, &c.Label, &stage, &c.TypeKey, &c.TypeVersion, &c.Position, &raw, &c.Enabled, &c.ParallelGroup, &c.Route); err != nil {
			return err
		}
		c.Stage = model.Stage(stage)
		c.Config = json.RawMessage(raw)
		wf.Components = append(wf.Components, c)
	}
	return rows.Err()
}

func scanWorkflow(row interface{ Scan(...any) error }) (*model.Workflow, error) {
	wf := &model.Workflow{}
	if err := row.Scan(&wf.ID, &wf.Name, &wf.Description, &wf.Enabled, &wf.Version,
		&wf.TriggerMode, &wf.CreatedBy, &wf.UpdatedBy, &wf.CreatedAt, &wf.UpdatedAt); err != nil {
		return nil, err
	}
	return wf, nil
}

func insertComponents(ctx context.Context, tx pgx.Tx, workflowID uuid.UUID, comps []model.Component) error {
	for i := range comps {
		c := &comps[i]
		config := c.Config
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO workflow.components
			(workflow_id, stage, label, type_key, type_version, position, config, enabled, parallel_group, route)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			RETURNING id`,
			workflowID, c.Stage, c.Label, c.TypeKey, c.TypeVersion, c.Position, config, c.Enabled, c.ParallelGroup, c.Route).
			Scan(&c.ID); err != nil {
			return err
		}
	}
	return nil
}

func normalizePositions(comps []model.Component) []model.Component {
	counts := map[model.Stage]int{}
	for i := range comps {
		comps[i].Position = counts[comps[i].Stage]
		counts[comps[i].Stage]++
	}
	return comps
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
