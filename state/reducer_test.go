package state

import "testing"

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
	if !ok || value != float64(5) {
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
	if !ok || value != float64(5) {
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
