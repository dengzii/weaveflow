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
	if err := view.SetSteps([]Step{{
		ID: "research", Title: "Research", ToolIDs: []string{"search"}, Deliverables: []string{"report"},
		AcceptanceCriteria: []string{"sources cited"}, VerificationStrategy: "evidence", VerificationStatus: "passed",
		VerificationSummary: "sources verified", VerificationAttempts: 2,
		Evidence:       []Evidence{{ToolID: "search", Status: "succeeded", Summary: "two sources"}},
		AttemptHistory: []Attempt{{Number: 1, VerificationStatus: "retry_step"}, {Number: 2, VerificationStatus: "passed"}},
		StartedAt:      "2026-08-21T00:00:00Z", DurationMillis: 1250, ModelCalls: 2, ToolCalls: 1,
		Status: "done", Result: "facts",
	}}); err != nil {
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
	if steps[0].VerificationStatus != "passed" || steps[0].VerificationAttempts != 2 || len(steps[0].Evidence) != 1 || len(steps[0].AttemptHistory) != 2 || steps[0].DurationMillis != 1250 || steps[0].ModelCalls != 2 || steps[0].ToolCalls != 1 {
		t.Fatalf("verification state = %#v", steps[0])
	}
	history := restoredView.History()
	if len(history) != 1 || history[0]["reason"] != "retry" || history[0]["count"] != 1 {
		t.Fatalf("history = %#v", history)
	}
}

func TestDecodeLegacyStepUsesSafeVerificationDefaults(t *testing.T) {
	steps := DecodeSteps([]map[string]any{{"id": "legacy", "title": "Legacy", "description": "old checkpoint"}})
	if len(steps) != 1 {
		t.Fatalf("steps = %#v", steps)
	}
	step := steps[0]
	if len(step.Deliverables) == 0 || len(step.AcceptanceCriteria) == 0 || step.VerificationStrategy != "evidence" || step.VerificationStatus != "pending" {
		t.Fatalf("legacy defaults = %#v", step)
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
