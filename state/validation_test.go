package state

import "testing"

func TestValidatePatchByContractRejectsUndeclaredWritesAndRequiresDeclaredWrites(t *testing.T) {
	t.Parallel()

	finalRef := NewRef[string](Shared("final", "answer")).Required()
	contract := NewContract(finalRef.WriteField())

	patch := NewPatch(
		PatchOp{Kind: OpSet, Path: Shared("final", "answer"), Value: "done"},
		PatchOp{Kind: OpSet, Path: Shared("planner", "status"), Value: "ready"},
	)

	issues := ValidatePatchByContract(patch, contract)
	if len(issues) != 1 {
		t.Fatalf("expected one issue, got %#v", issues)
	}
	if issues[0].Kind != "write_not_allowed" || issues[0].Path != "shared.planner.status" {
		t.Fatalf("unexpected issue %#v", issues[0])
	}
}

func TestValidatePatchByContractReportsMissingRequiredWrite(t *testing.T) {
	t.Parallel()

	finalRef := NewRef[string](Shared("final", "answer")).Required()
	contract := NewContract(finalRef.WriteField())
	patch := NewPatch(PatchOp{Kind: OpSet, Path: Shared("planner", "status"), Value: "ready"})

	issues := ValidatePatchByContract(patch, contract)
	if len(issues) != 2 {
		t.Fatalf("expected write_not_allowed and missing_required_write, got %#v", issues)
	}
	if issues[0].Kind != "write_not_allowed" || issues[1].Kind != "missing_required_write" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestValidateRequiredReads(t *testing.T) {
	t.Parallel()

	ref := NewRef[string](Shared("request", "input")).Required()
	contract := NewContract(ref.ReadField())

	missing := ValidateRequiredReads(NewState(), contract)
	if len(missing) != 1 || missing[0].Kind != "missing_required_read" {
		t.Fatalf("expected missing read issue, got %#v", missing)
	}

	present := FromShared(map[string]any{"request": map[string]any{"input": "hello"}})
	if issues := ValidateRequiredReads(present, contract); len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
}

func TestProjectStateByContractSelectsReadablePaths(t *testing.T) {
	t.Parallel()

	full := FromMap(map[string]any{
		SectionShared: map[string]any{
			"request": map[string]any{"input": "hello", "metadata": map[string]any{"source": "test"}},
			"final":   map[string]any{"answer": "done"},
		},
		SectionScopes: map[string]any{
			"agent": map[string]any{
				"conversation": map[string]any{"iteration_count": 2},
			},
		},
	})
	contract := NewContract(
		NewRef[string](Shared("request", "input")).ReadField(),
		NewRef[int](Scope("agent", "conversation", "iteration_count")).ReadField(),
		NewRef[string](Shared("final", "answer")).WriteField(),
	)

	projected := ProjectStateByContract(full, contract)
	access := NewAccess(nil, projected)

	input, ok := access.ReadAny(Shared("request", "input"))
	if !ok || input != "hello" {
		t.Fatalf("expected projected request input, got %#v ok=%v", input, ok)
	}
	iteration, ok := access.ReadAny(Scope("agent", "conversation", "iteration_count"))
	if !ok || iteration != 2 {
		t.Fatalf("expected projected iteration, got %#v ok=%v", iteration, ok)
	}
	if _, ok := access.ReadAny(Shared("final", "answer")); ok {
		t.Fatal("write-only final answer should not be projected")
	}
	if _, ok := access.ReadAny(Shared("request", "metadata")); ok {
		t.Fatal("undeclared request metadata should not be projected")
	}
}

func TestValidatePatchRejectsInvalidMergeValue(t *testing.T) {
	t.Parallel()

	issues := ValidatePatch(NewPatch(PatchOp{
		Kind:  OpMerge,
		Path:  Shared("planner"),
		Value: "not-map",
	}))
	if len(issues) != 1 || issues[0].Kind != "invalid_merge_value" {
		t.Fatalf("expected invalid merge issue, got %#v", issues)
	}
}
