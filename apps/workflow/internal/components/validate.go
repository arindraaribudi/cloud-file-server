package components

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/example/cos-workflow/internal/model"
)

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "validation failed"
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e.Fields[k])
	}
	return strings.Join(parts, "; ")
}

// parallelGroupPattern restricts group keys to a short, safe identifier.
var parallelGroupPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// routePattern restricts route keys the same way.
var routePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func newValidationError() *ValidationError { return &ValidationError{Fields: map[string]string{}} }

func (e *ValidationError) add(field, msg string) {
	if _, ok := e.Fields[field]; !ok {
		e.Fields[field] = msg
	}
}

func (e *ValidationError) errOrNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// ValidateWorkflow enforces stage assignment, config validity, trigger
// composition, execution ordering, and the enabled-component requirements.
func ValidateWorkflow(wf *model.Workflow, reg *Registry) error {
	ve := newValidationError()
	name := strings.TrimSpace(wf.Name)
	if len(name) == 0 || len(name) > 120 {
		ve.add("name", "must be 1-120 characters")
	}
	if wf.TriggerMode == "" {
		wf.TriggerMode = model.TriggerModeAny
	}
	if wf.TriggerMode != model.TriggerModeAny && wf.TriggerMode != model.TriggerModeAll {
		ve.add("trigger_mode", "must be any or all")
	}
	if len(wf.Components) == 0 {
		ve.add("components", "at least one component is required")
	}
	if len(wf.Components) > 50 {
		ve.add("components", "at most 50 components are allowed")
	}

	stageCount := map[model.Stage]int{}
	positions := map[model.Stage]map[int]bool{}
	enabledTriggers := 0
	triggerCount := 0
	enabledActions := 0
	triggerEventTypes := map[string]bool{}

	for i, c := range wf.Components {
		prefix := fmt.Sprintf("components[%d]", i)
		if len(c.Label) > 80 {
			ve.add(prefix+".label", "must be at most 80 characters")
		}
		switch c.Stage {
		case model.StageTrigger, model.StageProcess, model.StageOutput:
		default:
			ve.add(prefix+".stage", "must be trigger, process, or output")
			continue
		}
		stageCount[c.Stage]++
		if c.Stage == model.StageTrigger {
			triggerCount++
		}
		if positions[c.Stage] == nil {
			positions[c.Stage] = map[int]bool{}
		}
		if c.Position < 0 {
			ve.add(prefix+".position", "must be >= 0")
		} else if positions[c.Stage][c.Position] {
			ve.add(prefix+".position", "duplicate position in stage")
		} else {
			positions[c.Stage][c.Position] = true
		}

		if c.ParallelGroup != "" {
			if c.Stage != model.StageOutput {
				ve.add(prefix+".parallel_group", "only output components can run in parallel")
			} else if !parallelGroupPattern.MatchString(c.ParallelGroup) {
				ve.add(prefix+".parallel_group", "must be 1-64 characters (letters, numbers, . _ -)")
			}
		}

		if c.Route != "" {
			if c.Stage == model.StageTrigger {
				ve.add(prefix+".route", "triggers cannot belong to a route")
			} else if !routePattern.MatchString(c.Route) {
				ve.add(prefix+".route", "must be 1-64 characters (letters, numbers, . _ -)")
			}
		}

		t, ok := reg.Get(c.TypeKey, c.TypeVersion)
		if !ok {
			ve.add(prefix+".type", fmt.Sprintf("unknown component type/version %s v%d", c.TypeKey, c.TypeVersion))
			continue
		}
		if !allowedStage(t, c.Stage) {
			ve.add(prefix+".stage", fmt.Sprintf("%s cannot be assigned to %s", t.Key, c.Stage))
		}
		if err := t.Validate(c.Config); err != nil {
			ve.add(prefix+".config", err.Error())
		}
		if c.Enabled {
			if c.Stage == model.StageTrigger {
				enabledTriggers++
				if t.EventType != "" {
					triggerEventTypes[t.EventType] = true
				}
			} else {
				enabledActions++
			}
		}
	}

	for stage, count := range stageCount {
		if count > 20 {
			ve.add("components", fmt.Sprintf("at most 20 %s components are allowed", stage))
		}
	}
	if triggerCount > 1 {
		ve.add("components", "only one trigger component is allowed")
	}
	if enabledTriggers == 0 {
		ve.add("components", "at least one enabled trigger is required")
	}
	if enabledActions == 0 {
		ve.add("components", "at least one enabled process or output is required")
	}
	if wf.TriggerMode == model.TriggerModeAll && len(triggerEventTypes) > 1 {
		ve.add("trigger_mode", "all triggers must accept the same event type (upload or remove)")
	}

	// Remove is terminal for file-mutating components. Notifications after a
	// remove are allowed because they do not touch the file.
	process := make([]model.Component, 0)
	for _, c := range wf.Components {
		if c.Stage == model.StageProcess && c.Enabled {
			process = append(process, c)
		}
	}
	sort.Slice(process, func(i, j int) bool { return process[i].Position < process[j].Position })
	removed := false
	for _, c := range process {
		if removed {
			ve.add(fmt.Sprintf("components[%s]", c.ID), "no process component may follow a remove")
			continue
		}
		if c.TypeKey == TypeProcessRemove {
			removed = true
		}
	}
	if removed {
		for _, c := range wf.Components {
			if c.Stage == model.StageOutput && c.Enabled && c.TypeKey == TypeOutputCopy {
				ve.add(fmt.Sprintf("components[%s]", c.ID), "copy output cannot run after a remove")
			}
		}
	}
	return ve.errOrNil()
}
