package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

func newTestEngine() *Engine {
	return &Engine{Registry: components.NewRegistry(), Config: Config{MaxAttempts: 3}}
}

func triggerComp(pathRegex string) model.Component {
	return model.Component{
		ID: uuid.New(), Stage: model.StageTrigger, TypeKey: components.TypeTriggerUpload,
		TypeVersion: 1, Position: 0, Enabled: true,
		Config: json.RawMessage(`{"path_target":"basename","path_regex":"` + pathRegex + `"}`),
	}
}

func testWorkflow(mode string) *model.Workflow {
	return &model.Workflow{
		ID: uuid.New(), Name: "test", Version: 1, TriggerMode: mode,
		Components: []model.Component{
			triggerComp("^invoice_"),
			{
				ID: uuid.New(), Stage: model.StageProcess, TypeKey: components.TypeProcessRename,
				TypeVersion: 1, Position: 0, Enabled: true,
				Config: json.RawMessage(`{"mode":"template","template":"archive/{{.FileName}}","collision":"fail"}`),
			},
			{
				ID: uuid.New(), Stage: model.StageOutput, TypeKey: components.TypeOutputCopy,
				TypeVersion: 1, Position: 0, Enabled: true,
				Config: json.RawMessage(`{"destination_username":"","destination_path":"shared/{{.FileName}}","collision":"suffix"}`),
			},
		},
	}
}

func testEvent(path string) model.FileEvent {
	return model.FileEvent{
		ID: 1, EventTime: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		EventType: "upload", Username: "alice", Path: path,
		MIME: "application/pdf", Size: 10,
	}
}

func TestBuildRunMatchesAndSnapshots(t *testing.T) {
	e := newTestEngine()
	wf := testWorkflow(model.TriggerModeAny)
	run, comps, matched, err := e.buildRun(wf, &model.FileEvent{
		ID: 1, EventTime: time.Now(), EventType: "upload",
		Username: "alice", Path: "invoices/invoice_1.pdf",
		MIME: "application/pdf", Size: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected match")
	}
	if run.EventID == nil || *run.EventID != 1 {
		t.Fatalf("event id=%v", run.EventID)
	}
	var rc model.RunContext
	if err := json.Unmarshal(run.Context, &rc); err != nil {
		t.Fatal(err)
	}
	if rc.RunID != run.ID || rc.Username != "alice" || rc.Path != "invoices/invoice_1.pdf" {
		t.Fatalf("context=%+v", rc)
	}
	if len(comps) != 3 {
		t.Fatalf("components=%d", len(comps))
	}
	if comps[0].Status != model.ComponentStatusMatched {
		t.Errorf("trigger status=%s", comps[0].Status)
	}
	if comps[1].Status != model.ComponentStatusPending || comps[2].Status != model.ComponentStatusPending {
		t.Errorf("action statuses=%s/%s", comps[1].Status, comps[2].Status)
	}
}

func TestBuildRunNoMatch(t *testing.T) {
	e := newTestEngine()
	wf := testWorkflow(model.TriggerModeAny)
	_, _, matched, err := e.buildRun(wf, &model.FileEvent{
		ID: 1, EventTime: time.Now(), EventType: "upload",
		Username: "alice", Path: "other_1.pdf", MIME: "application/pdf", Size: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("expected no match")
	}
}

func TestBuildRunAllModeRequiresEveryTrigger(t *testing.T) {
	e := newTestEngine()
	wf := testWorkflow(model.TriggerModeAll)
	second := triggerComp("^other_")
	second.Position = 1
	wf.Components = append(wf.Components, second)

	event := testEvent("invoices/invoice_1.pdf")
	_, _, matched, err := e.buildRun(wf, &event)
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("all mode matched with only one trigger")
	}

	second = triggerComp("invoice")
	second.Position = 1
	wf.Components[3] = second
	_, _, matched, err = e.buildRun(wf, &event)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("all mode should match when every trigger matches")
	}
}

func TestBuildRunDisabledTriggerDoesNotMatch(t *testing.T) {
	e := newTestEngine()
	wf := testWorkflow(model.TriggerModeAny)
	wf.Components[0].Enabled = false
	event := testEvent("invoices/invoice_1.pdf")
	_, comps, matched, err := e.buildRun(wf, &event)
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("disabled trigger should not match")
	}
	if len(comps) != 0 {
		t.Fatalf("no run should be built, comps=%+v", comps)
	}
}
