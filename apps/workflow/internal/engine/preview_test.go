package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/example/cos-workflow/internal/action"
	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

func previewComp(stage model.Stage, typ, label, cfg string, enabled bool) model.Component {
	return model.Component{
		Stage: stage, TypeKey: typ, TypeVersion: 1, Label: label,
		Config: json.RawMessage(cfg), Enabled: enabled,
	}
}

func newPreviewEngine() *Engine {
	return &Engine{Actions: action.NewExecutor(nil, nil)}
}

func sampleEvent(path string) model.FileEvent {
	return model.FileEvent{Username: "alice", Path: path, MIME: "application/pdf", EventType: "upload", Size: 10}
}

func TestPreviewRenamePathCarriesIntoLaterSteps(t *testing.T) {
	wf := &model.Workflow{Components: []model.Component{
		previewComp(model.StageProcess, components.TypeProcessRename, "Rename",
			`{"mode":"template","template":"archive/{{.FileName}}","collision":"suffix"}`, true),
		previewComp(model.StageOutput, components.TypeOutputCopy, "Folder A",
			`{"destination_path":"a/{{.FileName}}","collision":"suffix"}`, true),
	}}
	steps := newPreviewEngine().Preview(context.Background(), wf, sampleEvent("in/report.pdf"))
	if len(steps) != 2 || steps[0].Status != "ok" || steps[1].Status != "ok" {
		t.Fatalf("steps=%+v", steps)
	}
	if got := steps[0].Output["destination_path"]; got != "archive/report.pdf" {
		t.Fatalf("rename destination=%v", got)
	}
	// the copy must start from the renamed file, as in a real run
	if got := steps[1].Output["source_path"]; got != "archive/report.pdf" {
		t.Fatalf("copy source=%v, want the renamed path", got)
	}
}

func TestPreviewConditionNotMetSkipsRest(t *testing.T) {
	wf := &model.Workflow{Components: []model.Component{
		previewComp(model.StageProcess, components.TypeProcessCondition, "Only scans",
			`{"path_target":"basename","path_regex":"^scan_"}`, true),
		previewComp(model.StageOutput, components.TypeOutputCopy, "Folder A",
			`{"destination_path":"a/{{.FileName}}","collision":"suffix"}`, true),
	}}
	steps := newPreviewEngine().Preview(context.Background(), wf, sampleEvent("in/invoice.pdf"))
	if steps[0].Status != "ok" || steps[0].Output["condition"] != false {
		t.Fatalf("condition step=%+v", steps[0])
	}
	if steps[1].Status != "skipped" || steps[1].Reason == "" {
		t.Fatalf("copy should be skipped, got %+v", steps[1])
	}
}

func routed(c model.Component, route string) model.Component {
	c.Route = route
	return c
}

func TestPreviewRoutesAreIndependent(t *testing.T) {
	wf := &model.Workflow{Components: []model.Component{
		// route A: scans
		routed(previewComp(model.StageProcess, components.TypeProcessCondition, "Is scan",
			`{"path_target":"basename","path_regex":"^scan_"}`, true), "a"),
		routed(previewComp(model.StageOutput, components.TypeOutputCopy, "Scans folder",
			`{"destination_path":"scans/{{.FileName}}","collision":"suffix"}`, true), "a"),
		// route B: invoices
		routed(previewComp(model.StageProcess, components.TypeProcessCondition, "Is invoice",
			`{"path_target":"basename","path_regex":"^invoice_"}`, true), "b"),
		routed(previewComp(model.StageOutput, components.TypeOutputCopy, "Invoices folder",
			`{"destination_path":"invoices/{{.FileName}}","collision":"suffix"}`, true), "b"),
	}}
	steps := newPreviewEngine().Preview(context.Background(), wf, sampleEvent("in/invoice_7.pdf"))
	byLabel := map[string]PreviewStep{}
	for _, s := range steps {
		byLabel[s.Label] = s
	}
	if byLabel["Is scan"].Output["condition"] != false {
		t.Fatalf("route A condition should not match: %+v", byLabel["Is scan"])
	}
	if byLabel["Scans folder"].Status != "skipped" {
		t.Fatalf("route A action must be skipped: %+v", byLabel["Scans folder"])
	}
	// the other route must be untouched by route A not matching
	if byLabel["Invoices folder"].Status != "ok" {
		t.Fatalf("route B action must still run: %+v", byLabel["Invoices folder"])
	}
}

func TestPreviewBothRoutesCanMatch(t *testing.T) {
	wf := &model.Workflow{Components: []model.Component{
		routed(previewComp(model.StageProcess, components.TypeProcessCondition, "pdf",
			`{"path_target":"basename","mime_patterns":["application/pdf"]}`, true), "a"),
		routed(previewComp(model.StageOutput, components.TypeOutputCopy, "A",
			`{"destination_path":"a/{{.FileName}}","collision":"suffix"}`, true), "a"),
		routed(previewComp(model.StageProcess, components.TypeProcessCondition, "invoice",
			`{"path_target":"basename","path_regex":"^invoice_"}`, true), "b"),
		routed(previewComp(model.StageOutput, components.TypeOutputCopy, "B",
			`{"destination_path":"b/{{.FileName}}","collision":"suffix"}`, true), "b"),
	}}
	for _, s := range newPreviewEngine().Preview(context.Background(), wf, sampleEvent("in/invoice_1.pdf")) {
		if s.Status != "ok" {
			t.Fatalf("every route matches, but %q is %s", s.Label, s.Status)
		}
	}
}

func TestPreviewDisabledAndInvalidSteps(t *testing.T) {
	wf := &model.Workflow{Components: []model.Component{
		previewComp(model.StageOutput, components.TypeOutputCopy, "Off",
			`{"destination_path":"a/{{.FileName}}","collision":"suffix"}`, false),
		previewComp(model.StageOutput, components.TypeOutputCopy, "Unfinished", `{"collision":"suffix"}`, true),
		previewComp(model.StageOutput, components.TypeOutputCopy, "Fine",
			`{"destination_path":"b/{{.FileName}}","collision":"suffix"}`, true),
	}}
	steps := newPreviewEngine().Preview(context.Background(), wf, sampleEvent("in/x.pdf"))
	if steps[0].Status != "skipped" {
		t.Fatalf("disabled step=%+v", steps[0])
	}
	if steps[1].Status != "failed" || steps[1].Error == "" {
		t.Fatalf("unfinished step should report its own error, got %+v", steps[1])
	}
	// one bad step must not hide the others
	if steps[2].Status != "ok" {
		t.Fatalf("later step=%+v", steps[2])
	}
}
