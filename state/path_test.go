package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPathJSONMarshalsAsString(t *testing.T) {
	t.Parallel()

	path := Shared("final", "answer")
	raw, err := json.Marshal(path)
	if err != nil {
		t.Fatalf("marshal path: %v", err)
	}
	if string(raw) != `"shared.final.answer"` {
		t.Fatalf("unexpected path json %s", raw)
	}
}

func TestContractJSONIncludesReadablePath(t *testing.T) {
	t.Parallel()

	contract := NewContract(NewRef[string](Shared("final", "answer")).ReadWriteField())
	raw, err := json.Marshal(contract)
	if err != nil {
		t.Fatalf("marshal contract: %v", err)
	}
	if !strings.Contains(string(raw), `"shared.final.answer"`) {
		t.Fatalf("expected contract json to include path string, got %s", raw)
	}
}

func TestEmptyPathWritesAreRejected(t *testing.T) {
	t.Parallel()

	if err := NewState().set(Path{}, map[string]any{"x": true}); err == nil || !strings.Contains(err.Error(), "state path is required") {
		t.Fatalf("expected empty state path error, got %v", err)
	}

	_, err := NewPatch(PatchOp{Kind: OpSet, Path: Path{}, Value: map[string]any{"x": true}}).Apply(NewState())
	if err == nil || !strings.Contains(err.Error(), "patch op path is required") {
		t.Fatalf("expected empty patch path error, got %v", err)
	}
}
