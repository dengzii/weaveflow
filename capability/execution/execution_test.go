package execution

import (
	"testing"

	"github.com/dengzii/weaveflow/state"
)

type checkpointBusinessStep struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
}

func TestDefinitionFieldsMatchViewRefs(t *testing.T) {
	t.Parallel()
	root := state.Scope("worker", "execution")
	view, err := Bind(state.NewAccess(state.NewState()), root)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	definition := Definition()
	want := map[string]string{
		FieldCurrentStep: root.MustChild(FieldCurrentStep).String(),
		FieldStepResults: root.MustChild(FieldStepResults).String(),
		FieldLastLLMStep: root.MustChild(FieldLastLLMStep).String(),
	}
	if len(definition.Fields) != len(want) {
		t.Fatalf("definition fields = %#v", definition.Fields)
	}
	for _, field := range definition.Fields {
		if _, ok := want[field.Name]; !ok {
			t.Fatalf("unexpected definition field %q", field.Name)
		}
	}
	if view.currentStepRef.Path().String() != want[FieldCurrentStep] ||
		view.stepResultsRef.Path().String() != want[FieldStepResults] ||
		view.lastLLMStepRef.Path().String() != want[FieldLastLLMStep] {
		t.Fatalf("view refs do not match definition: %#v", want)
	}
}

func TestViewReadsExplicitJSONBusinessShapeAfterSnapshotRoundTrip(t *testing.T) {
	t.Parallel()
	root := state.Shared("workflow", "execution")
	access := state.NewEditingAccess(state.NewState())
	view, err := Bind(access, root)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := view.SetCurrentStep(map[string]any{
		"step": checkpointBusinessStep{ID: "research", Done: true},
	}); err != nil {
		t.Fatalf("set current step: %v", err)
	}
	if err := view.SetStepResult("research", map[string]any{"answer": "done"}); err != nil {
		t.Fatalf("set step result: %v", err)
	}

	snapshot, err := state.SnapshotFromState(access.State())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	codec := state.NewJSONStateCodec("")
	payload, err := codec.Encode(snapshot)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := codec.Decode(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	restored, err := state.FromSnapshot(decoded)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	restoredView, err := Bind(state.NewAccess(restored), root)
	if err != nil {
		t.Fatalf("bind restored: %v", err)
	}

	step, ok := restoredView.CurrentStep()["step"].(map[string]any)
	if !ok {
		t.Fatalf("restored step type = %T", restoredView.CurrentStep()["step"])
	}
	if step["id"] != "research" || step["done"] != true {
		t.Fatalf("restored step = %#v", step)
	}
	result, ok := restoredView.StepResults()["research"].(map[string]any)
	if !ok || result["answer"] != "done" {
		t.Fatalf("restored result = %#v", restoredView.StepResults()["research"])
	}
}
