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

func TestValidatePatchByContractDoesNotGrantDescendantWriteAccess(t *testing.T) {
	t.Parallel()
	contract := NewContract(FieldAccess{
		Path: Shared("output"), Mode: AccessWrite, Merge: MergeReplace,
	})
	issues := ValidatePatchByContract(NewPatch(PatchOp{
		Kind: OpSet, Path: Shared("output", "hidden"), Value: true,
	}), contract)
	if len(issues) != 1 || issues[0].Kind != "write_not_allowed" || issues[0].Path != "shared.output.hidden" {
		t.Fatalf("expected exact-path write rejection, got %#v", issues)
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

func TestValidateJSONSchemaValueReportsNestedObjectAndArrayPath(t *testing.T) {
	t.Parallel()

	schema := JSONSchema{
		"type":     "object",
		"required": []string{"items"},
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"name"},
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	value := map[string]any{
		"items": []any{
			map[string]any{"name": "valid"},
			map[string]any{"name": 42},
		},
	}

	issues := ValidateJSONSchemaValue(value, schema, "shared.payload")
	if len(issues) != 1 {
		t.Fatalf("issues = %#v, want one nested diagnostic", issues)
	}
	if issues[0].Path != "shared.payload.items.1.name" {
		t.Fatalf("issue path = %q, want nested array item path", issues[0].Path)
	}
	if issues[0].Kind == "" || issues[0].Message == "" {
		t.Fatalf("issue lacks structured diagnostic: %#v", issues[0])
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
	access := NewAccess(projected)

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

func TestContractBoundaryRejectsReservedPathsAndWildcardLeaks(t *testing.T) {
	t.Parallel()

	contract := NewContract(FieldAccess{Path: Internal("scheduler", "state"), Mode: AccessReadWrite})
	issues := ValidateContract(contract)
	if len(issues) != 1 || issues[0].Kind != "reserved_contract_path" {
		t.Fatalf("reserved contract issues = %#v", issues)
	}

	full := FromMap(map[string]any{
		SectionShared:   map[string]any{"value": "ok"},
		SectionInternal: map[string]any{"secret": "must not leak"},
		SectionRuntime:  map[string]any{"checkpoint": "must not leak"},
	})
	wildcard := Contract{WildcardRead: true, WildcardWrite: true}
	projected := ProjectStateByContract(full, wildcard)
	if _, ok := ReadPath(projected, "internal.secret"); ok {
		t.Fatal("wildcard projection exposed internal state")
	}
	if _, ok := ReadPath(projected, "runtime.checkpoint"); ok {
		t.Fatal("wildcard projection exposed runtime state")
	}
	patchIssues := ValidatePatchByContract(NewPatch(PatchOp{
		Kind: OpSet, Path: Runtime("checkpoint"), Value: "tampered",
	}), wildcard)
	if len(patchIssues) != 1 || patchIssues[0].Kind != "reserved_patch_path" {
		t.Fatalf("wildcard patch issues = %#v", patchIssues)
	}
}

func TestValidateInputPatchRejectsReservedPathsAndInvalidContracts(t *testing.T) {
	t.Parallel()

	patch := NewPatch(PatchOp{Kind: OpSet, Path: Internal("scheduler", "state"), Value: "tampered"})
	patchIssues := ValidateInputPatch(patch)
	if len(patchIssues) != 1 || patchIssues[0].Kind != "reserved_input_path" {
		t.Fatalf("input patch issues = %#v", patchIssues)
	}

	contract := NewContract(FieldAccess{Path: Internal("scheduler", "state"), Mode: AccessReadWrite})
	issues := ValidateInputPatchByContractWithReducers(
		NewState(),
		patch,
		contract,
		nil,
	)
	seen := map[string]bool{}
	for _, issue := range issues {
		seen[issue.Kind] = true
	}
	if !seen["reserved_contract_path"] || !seen["reserved_input_path"] {
		t.Fatalf("input validation issues = %#v", issues)
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
