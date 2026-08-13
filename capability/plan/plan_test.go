package plan

import (
	"reflect"
	"testing"

	"github.com/dengzii/weaveflow/state"
)

func TestDefinitionFieldsMatchViewFields(t *testing.T) {
	t.Parallel()
	want := fieldOrder()
	definition := Definition()
	got := make([]string, 0, len(definition.Fields))
	for _, field := range definition.Fields {
		got = append(got, field.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("definition fields = %#v, want %#v", got, want)
	}
}

func TestViewRestoresTypedStepsAndHistoryAfterJSONCheckpoint(t *testing.T) {
	t.Parallel()
	root := state.Scope("planner", "custom_plan")
	access := state.NewEditingAccess(state.NewState())
	view, _ := Bind(access, root)
	if err := view.SetSteps([]Step{{ID: "research", Title: "Research", ToolIDs: []string{"search"}, Status: "done", Result: "facts"}}); err != nil {
		t.Fatalf("set steps: %v", err)
	}
	if err := view.SetHistory([]map[string]any{{"reason": "retry", "count": 1}}); err != nil {
		t.Fatalf("set history: %v", err)
	}

	restored := roundTripState(t, access.State())
	restoredView, _ := Bind(state.NewAccess(restored), root)
	steps := restoredView.Steps()
	if len(steps) != 1 || steps[0].ID != "research" || steps[0].ToolIDs[0] != "search" || steps[0].Result != "facts" {
		t.Fatalf("steps = %#v", steps)
	}
	history := restoredView.History()
	if len(history) != 1 || history[0]["reason"] != "retry" || history[0]["count"] != 1 {
		t.Fatalf("history = %#v", history)
	}
}

func roundTripState(t *testing.T, input *state.State) *state.State {
	t.Helper()
	snapshot, err := state.SnapshotFromState(input)
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
	return restored
}
