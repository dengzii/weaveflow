package supervisor

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

func TestViewRestoresTypedHistoryAfterJSONCheckpoint(t *testing.T) {
	t.Parallel()
	root := state.Scope("orchestrator", "custom_supervisor")
	access := state.NewEditingAccess(state.NewState())
	view, _ := Bind(access, root)
	if err := view.SetHistory([]Turn{{Turn: 2, WorkerID: "writer", Task: "draft", Result: "done"}}); err != nil {
		t.Fatalf("set history: %v", err)
	}

	restored := roundTripState(t, access.State())
	restoredView, _ := Bind(state.NewAccess(restored), root)
	history := restoredView.History()
	if len(history) != 1 || history[0].Turn != 2 || history[0].WorkerID != "writer" || history[0].Result != "done" {
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
