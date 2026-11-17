package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Stage string

const (
	StageTrigger Stage = "trigger"
	StageProcess Stage = "process"
	StageOutput  Stage = "output"
)

const (
	TriggerModeAny = "any"
	TriggerModeAll = "all"
)

const (
	RunStatusQueued    = "queued"
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCanceled  = "canceled"
)

const (
	ComponentStatusMatched    = "matched"
	ComponentStatusNotMatched = "not_matched"
	ComponentStatusPending    = "pending"
	ComponentStatusRunning    = "running"
	ComponentStatusSucceeded  = "succeeded"
	ComponentStatusFailed     = "failed"
	ComponentStatusSkipped    = "skipped"
)

type Workflow struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Enabled     bool        `json:"enabled"`
	Version     int         `json:"version"`
	TriggerMode string      `json:"trigger_mode"`
	Components  []Component `json:"components"`
	CreatedBy   string      `json:"created_by"`
	UpdatedBy   string      `json:"updated_by"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type Component struct {
	ID          uuid.UUID       `json:"id"`
	Label       string          `json:"label"`
	Stage       Stage           `json:"stage"`
	TypeKey     string          `json:"type"`
	TypeVersion int             `json:"type_version"`
	Position    int             `json:"position"`
	Config      json.RawMessage `json:"config"`
	Enabled     bool            `json:"enabled"`
	// ParallelGroup, when set on adjacent output components, makes them run
	// concurrently instead of one after another.
	ParallelGroup string `json:"parallel_group"`
	// Route ties a step to one Condition. A condition that is not met skips the
	// remaining steps of its own route only.
	Route string `json:"route"`
}

type FileEvent struct {
	ID              int64     `json:"id"`
	EventTime       time.Time `json:"event_time"`
	EventType       string    `json:"event_type"`
	UserID          int64     `json:"user_id"`
	Username        string    `json:"username"`
	RootFolder      string    `json:"root_folder"`
	ConnectionType  string    `json:"connection_type"`
	BackendLocation string    `json:"backend_location"`
	Path            string    `json:"path"`
	OldPath         string    `json:"old_path,omitempty"`
	IsDir           bool      `json:"is_dir"`
	Size            int64     `json:"size"`
	MIME            string    `json:"mime"`
	MIMESource      string    `json:"mime_source"`
	Overwrite       bool      `json:"overwrite"`
	SessionID       uuid.UUID `json:"session_id"`
}

type RunContext struct {
	RunID        uuid.UUID `json:"run_id"`
	WorkflowID   uuid.UUID `json:"workflow_id"`
	Username     string    `json:"username"`
	UserID       int64     `json:"user_id"`
	RootFolder   string    `json:"root_folder"`
	Path         string    `json:"path"`
	OriginalPath string    `json:"original_path"`
	EventType    string    `json:"event_type"`
	EventTime    time.Time `json:"event_time"`
	MIME         string    `json:"mime"`
	Size         int64     `json:"size"`
	IsDir        bool      `json:"is_dir"`
}

type Run struct {
	ID              uuid.UUID       `json:"id"`
	WorkflowID      uuid.UUID       `json:"workflow_id"`
	WorkflowVersion int             `json:"workflow_version"`
	TriggerMode     string          `json:"trigger_mode"`
	EventID         *int64          `json:"event_id,omitempty"`
	Source          string          `json:"source"`
	Status          string          `json:"status"`
	Event           json.RawMessage `json:"event"`
	Context         json.RawMessage `json:"context"`
	Error           string          `json:"error"`
	Attempt         int             `json:"attempt"`
	MaxAttempts     int             `json:"max_attempts"`
	CancelRequested bool            `json:"cancel_requested"`
	NextAttemptAt   *time.Time      `json:"next_attempt_at,omitempty"`
	LockedBy        string          `json:"locked_by,omitempty"`
	LockedUntil     *time.Time      `json:"locked_until,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

type RunComponent struct {
	ID            uuid.UUID       `json:"id"`
	RunID         uuid.UUID       `json:"run_id"`
	ComponentID   *uuid.UUID      `json:"component_id,omitempty"`
	Stage         Stage           `json:"stage"`
	Label         string          `json:"label"`
	TypeKey       string          `json:"type"`
	TypeVersion   int             `json:"type_version"`
	Position      int             `json:"position"`
	Config        json.RawMessage `json:"config"`
	ParallelGroup string          `json:"parallel_group"`
	Route         string          `json:"route"`
	Status        string          `json:"status"`
	Attempt       int             `json:"attempt"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	Error         string          `json:"error"`
	Output        json.RawMessage `json:"output"`
}
