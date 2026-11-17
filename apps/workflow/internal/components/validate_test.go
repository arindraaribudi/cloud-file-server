package components

import (
	"encoding/json"
	"testing"

	"github.com/example/cos-workflow/internal/model"
)

func validWorkflow() *model.Workflow {
	return &model.Workflow{
		Name:        "archive invoices",
		TriggerMode: model.TriggerModeAny,
		Components: []model.Component{
			{Stage: model.StageTrigger, TypeKey: TypeTriggerUpload, TypeVersion: 1, Position: 0, Config: defaultTriggerConfig, Enabled: true},
			{Stage: model.StageProcess, TypeKey: TypeProcessRename, TypeVersion: 1, Position: 0, Config: defaultRenameConfig, Enabled: true},
			{Stage: model.StageOutput, TypeKey: TypeOutputCopy, TypeVersion: 1, Position: 0, Config: defaultCopyConfig, Enabled: true},
		},
	}
}

func TestValidateWorkflowValid(t *testing.T) {
	if err := ValidateWorkflow(validWorkflow(), NewRegistry()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWorkflowRequiresEnabledTriggerAndAction(t *testing.T) {
	wf := validWorkflow()
	wf.Components[0].Enabled = false
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected error for no enabled trigger")
	}

	wf = validWorkflow()
	wf.Components[1].Enabled = false
	wf.Components[2].Enabled = false
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected error for no enabled action")
	}
}

func TestValidateWorkflowStageMismatch(t *testing.T) {
	wf := validWorkflow()
	wf.Components[0].Stage = model.StageProcess
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected stage mismatch")
	}
}

func TestValidateWorkflowRemoveTerminalCopy(t *testing.T) {
	wf := validWorkflow()
	wf.Components[1] = model.Component{
		Stage: model.StageProcess, TypeKey: TypeProcessRemove, TypeVersion: 1,
		Position: 0, Config: defaultRemoveConfig, Enabled: true,
	}
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected copy-after-remove error")
	}
	wf.Components[2] = model.Component{
		Stage: model.StageOutput, TypeKey: TypeOutputEmail, TypeVersion: 1,
		Position: 0, Config: json.RawMessage(`{"to":["ops@example.com"],"subject_template":"done","body_template":"ok"}`), Enabled: true,
	}
	if err := ValidateWorkflow(wf, NewRegistry()); err != nil {
		t.Fatalf("email after remove should be allowed: %v", err)
	}
}

func TestValidateWorkflowAllModeRejectsMixedEventTypes(t *testing.T) {
	wf := validWorkflow()
	wf.TriggerMode = model.TriggerModeAll
	wf.Components = append(wf.Components, model.Component{
		Stage: model.StageTrigger, TypeKey: TypeTriggerRemove, TypeVersion: 1,
		Position: 1, Config: defaultTriggerConfig, Enabled: true,
	})
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected mixed event type error")
	}
}

func TestValidateWorkflowParallelGroup(t *testing.T) {
	wf := validWorkflow()
	wf.Components[len(wf.Components)-1].ParallelGroup = "copy"
	last := wf.Components[len(wf.Components)-1]
	if last.Stage != model.StageOutput {
		t.Skip("fixture does not end with an output component")
	}
	if err := ValidateWorkflow(wf, NewRegistry()); err != nil {
		t.Fatalf("valid parallel group rejected: %v", err)
	}
	wf.Components[len(wf.Components)-1].ParallelGroup = "bad group!"
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected error for invalid parallel_group")
	}
	wf.Components[len(wf.Components)-1].ParallelGroup = ""
	wf.Components[0].ParallelGroup = "copy" // trigger
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected error for parallel_group on a non-output component")
	}
}

func TestValidateWorkflowOnlyOneTrigger(t *testing.T) {
	wf := validWorkflow()
	wf.Components = append(wf.Components, model.Component{
		Stage: model.StageTrigger, TypeKey: TypeTriggerRemove, TypeVersion: 1,
		Position: 1, Config: defaultTriggerConfig, Enabled: true,
	})
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected error for more than one trigger")
	}
}

func TestValidateWorkflowUnknownType(t *testing.T) {
	wf := validWorkflow()
	wf.Components[0].TypeKey = "missing.type"
	if err := ValidateWorkflow(wf, NewRegistry()); err == nil {
		t.Fatal("expected unknown type error")
	}
}
