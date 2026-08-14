package state

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func TestReducerAppliesSequentially(t *testing.T) {
	patch := NewPatch(
		PatchOp{Kind: OpReduce, Path: Shared("total"), Reducer: "sum.v1", Value: 2},
		PatchOp{Kind: OpReduce, Path: Shared("total"), Reducer: "sum.v1", Value: 3},
	)
	result, err := patch.ApplyWithReducers(NewState(), map[string]Reducer{"sum.v1": SumReducer{}})
	if err != nil {
		t.Fatalf("apply reducer patch: %v", err)
	}
	value, ok := ReadPath(result, "shared.total")
	if !ok || value != 5 {
		t.Fatalf("total = %#v, want 5", value)
	}
}

func TestReducerRejectsUnknownIdentifier(t *testing.T) {
	patch := NewPatch(PatchOp{Kind: OpReduce, Path: Shared("total"), Reducer: "missing.v1", Value: 1})
	if _, err := patch.Apply(NewState()); err == nil {
		t.Fatal("expected unknown reducer error")
	}
}

func TestReducerContractRejectsMismatchedOperation(t *testing.T) {
	contract := NewContract(FieldAccess{Path: Shared("total"), Mode: AccessWrite, Merge: MergeReplace, Reducer: "sum.v1"})
	patch := NewPatch(PatchOp{Kind: OpSet, Path: Shared("total"), Value: 1})
	issues := ValidatePatchByContract(patch, contract)
	if len(issues) != 1 || issues[0].Kind != "reducer_mismatch" {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestReducerMergesParallelBranchesDeterministically(t *testing.T) {
	contract := NewContract(FieldAccess{Path: Shared("total"), Mode: AccessWrite, Merge: MergeReplace, Reducer: "sum.v1"})
	branches := []BranchPatch{
		{TaskID: "second", NodeID: "worker", Order: 2, Patch: NewPatch(PatchOp{Kind: OpReduce, Path: Shared("total"), Reducer: "sum.v1", Value: 3})},
		{TaskID: "first", NodeID: "worker", Order: 1, Patch: NewPatch(PatchOp{Kind: OpReduce, Path: Shared("total"), Reducer: "sum.v1", Value: 2})},
	}
	result, err := MergeParallelPatches(NewState(), branches, ParallelMergeOptions{
		Contracts: map[string]Contract{"worker": contract},
		Reducers:  map[string]Reducer{"sum.v1": SumReducer{}},
	})
	if err != nil {
		t.Fatalf("merge reducer branches: %v", err)
	}
	value, ok := ReadPath(result, "shared.total")
	if !ok || value != 5 {
		t.Fatalf("total = %#v, want 5", value)
	}
}

func TestMessagesReducerPreservesConcreteSliceType(t *testing.T) {
	value, err := (MessagesReducer{}).Reduce([]string{"a"}, []string{"b"})
	if err != nil {
		t.Fatalf("reduce messages: %v", err)
	}
	messages, ok := value.([]string)
	if !ok || len(messages) != 2 || messages[0] != "a" || messages[1] != "b" {
		t.Fatalf("messages = %#v", value)
	}
}

func TestReducerRejectsTypedNilImplementation(t *testing.T) {
	var reducer *pointerReducer
	patch := NewPatch(PatchOp{Kind: OpReduce, Path: Shared("total"), Reducer: "nil.v1", Value: 1})
	if _, err := patch.ApplyWithReducers(NewState(), map[string]Reducer{"nil.v1": reducer}); err == nil || !strings.Contains(err.Error(), `unknown state reducer "nil.v1"`) {
		t.Fatalf("typed nil reducer error = %v", err)
	}
}

func TestAppendRejectsExistingNonSliceValue(t *testing.T) {
	base := FromShared(map[string]any{"items": "keep"})
	patch := NewPatch(PatchOp{Kind: OpAppend, Path: Shared("items"), Value: "new"})
	if _, err := patch.Apply(base); err == nil || !strings.Contains(err.Error(), "want slice") {
		t.Fatalf("append scalar error = %v", err)
	}
	if value, _ := ReadPath(base, "shared.items"); value != "keep" {
		t.Fatalf("base state was changed to %#v", value)
	}
}

func TestNumericReducersPreserveLargeIntegerPrecision(t *testing.T) {
	const lower int64 = 9007199254740992
	const higher int64 = 9007199254740993

	sum, err := (SumReducer{}).Reduce(higher, int64(2))
	if err != nil {
		t.Fatalf("sum reducer: %v", err)
	}
	if fmt.Sprint(sum) != "9007199254740995" {
		t.Fatalf("sum = %#v", sum)
	}
	if _, ok := sum.(float64); ok {
		t.Fatalf("integer sum was converted to float64: %#v", sum)
	}

	maximum, err := (MaxReducer{}).Reduce(lower, higher)
	if err != nil {
		t.Fatalf("max reducer: %v", err)
	}
	if maximum != higher {
		t.Fatalf("integer max = %#v, want %d", maximum, higher)
	}
	mixedMaximum, err := (MaxReducer{}).Reduce(float64(lower), higher)
	if err != nil {
		t.Fatalf("mixed max reducer: %v", err)
	}
	if mixedMaximum != higher {
		t.Fatalf("mixed max = %#v, want %d", mixedMaximum, higher)
	}
}

func TestReducerPreservesLargeIntegerFromJSON(t *testing.T) {
	var decoded State
	if err := json.Unmarshal([]byte(`{"shared":{"total":9007199254740993e0}}`), &decoded); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	var patch Patch
	if err := json.Unmarshal([]byte(`[{"kind":"reduce","path":"shared.total","value":2e0,"reducer":"sum.v1"}]`), &patch); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	result, err := patch.ApplyWithReducers(&decoded, map[string]Reducer{"sum.v1": SumReducer{}})
	if err != nil {
		t.Fatalf("apply reducer patch: %v", err)
	}
	value, ok := ReadPath(result, "shared.total")
	if !ok || fmt.Sprint(value) != "9007199254740995" {
		t.Fatalf("JSON total = %#v", value)
	}
}

func TestStateReadClonesLargeInteger(t *testing.T) {
	const original = "184467440737095516160"
	var decoded State
	if err := json.Unmarshal([]byte(`{"shared":{"total":`+original+`}}`), &decoded); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	value, ok := ReadPath(&decoded, "shared.total")
	if !ok {
		t.Fatal("large integer is missing")
	}
	integer, ok := value.(*big.Int)
	if !ok {
		t.Fatalf("large integer type = %T, want *big.Int", value)
	}
	integer.SetInt64(1)
	persisted, ok := ReadPath(&decoded, "shared.total")
	if !ok || fmt.Sprint(persisted) != original {
		t.Fatalf("state value changed through read clone: %#v", persisted)
	}
}

type pointerReducer struct{}

func (*pointerReducer) Reduce(current, incoming any) (any, error) {
	return incoming, nil
}
