package engine

import (
	"testing"

	"github.com/example/cos-workflow/internal/model"
)

func rc(stage model.Stage, group string) model.RunComponent {
	return model.RunComponent{Stage: stage, ParallelGroup: group}
}

func TestBatchComponentsGroupsAdjacentParallelOutputs(t *testing.T) {
	comps := []model.RunComponent{
		rc(model.StageTrigger, ""),
		rc(model.StageProcess, ""),
		rc(model.StageOutput, "copy"),
		rc(model.StageOutput, "copy"),
		rc(model.StageOutput, "copy"),
		rc(model.StageOutput, "hook"),
		rc(model.StageOutput, "hook"),
		rc(model.StageOutput, ""),
	}
	got := batchComponents(comps)
	sizes := make([]int, len(got))
	for i, b := range got {
		sizes[i] = len(b)
	}
	want := []int{1, 3, 2, 1} // process, copy x3, hook x2, plain output
	if len(sizes) != len(want) {
		t.Fatalf("batches=%v want %v", sizes, want)
	}
	for i := range want {
		if sizes[i] != want[i] {
			t.Fatalf("batches=%v want %v", sizes, want)
		}
	}
}

func TestBatchComponentsNeverMergesAcrossRoutes(t *testing.T) {
	a := rc(model.StageOutput, "copy")
	a.Route = "a"
	b := rc(model.StageOutput, "copy")
	b.Route = "b"
	a2 := rc(model.StageOutput, "copy")
	a2.Route = "a"
	got := batchComponents([]model.RunComponent{a, a2, b})
	if len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 1 {
		t.Fatalf("batches=%v, want route a's two copies together and route b alone", got)
	}
}

func TestBatchComponentsDoesNotMergeAcrossGroupsOrSequential(t *testing.T) {
	comps := []model.RunComponent{
		rc(model.StageOutput, "copy"),
		rc(model.StageOutput, ""),
		rc(model.StageOutput, "copy"),
	}
	if got := batchComponents(comps); len(got) != 3 {
		t.Fatalf("batches=%d want 3", len(got))
	}
}
