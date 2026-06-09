package state

import (
	"strings"
	"testing"
)

func TestMergeParallelPatchesAppendsInDeterministicBranchOrder(t *testing.T) {
	t.Parallel()

	base := FromShared(map[string]any{"items": []string{"base"}})
	merged, err := MergeParallelPatches(base, []BranchPatch{
		{
			NodeID: "worker-b",
			Order:  20,
			Patch:  NewPatch(PatchOp{Kind: OpAppend, Path: Shared("items"), Value: "b"}),
		},
		{
			NodeID: "worker-a",
			Order:  10,
			Patch:  NewPatch(PatchOp{Kind: OpAppend, Path: Shared("items"), Value: "a"}),
		},
	}, ParallelMergeOptions{})
	if err != nil {
		t.Fatalf("merge parallel patches: %v", err)
	}

	itemsValue, ok := NewAccess(nil, merged).ReadAny(Shared("items"))
	if !ok {
		t.Fatal("expected merged items")
	}
	items, ok := itemsValue.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", itemsValue)
	}
	if len(items) != 3 || items[0] != "base" || items[1] != "a" || items[2] != "b" {
		t.Fatalf("unexpected items %#v", items)
	}
}

func TestMergeParallelPatchesMergesDisjointObjectKeys(t *testing.T) {
	t.Parallel()

	base := FromShared(map[string]any{
		"meta": map[string]any{"keep": true},
	})
	merged, err := MergeParallelPatches(base, []BranchPatch{
		{
			NodeID: "left",
			Order:  1,
			Patch: NewPatch(PatchOp{
				Kind:  OpMerge,
				Path:  Shared("meta"),
				Value: map[string]any{"left": "ok"},
			}),
		},
		{
			NodeID: "right",
			Order:  2,
			Patch: NewPatch(PatchOp{
				Kind:  OpMerge,
				Path:  Shared("meta"),
				Value: map[string]any{"right": "ok"},
			}),
		},
	}, ParallelMergeOptions{})
	if err != nil {
		t.Fatalf("merge parallel patches: %v", err)
	}

	metaValue, ok := NewAccess(nil, merged).ReadAny(Shared("meta"))
	if !ok {
		t.Fatal("expected merged meta")
	}
	meta := metaValue.(map[string]any)
	if meta["keep"] != true || meta["left"] != "ok" || meta["right"] != "ok" {
		t.Fatalf("unexpected meta %#v", meta)
	}
}

func TestMergeParallelPatchesRejectsConflictingSetWrites(t *testing.T) {
	t.Parallel()

	_, err := MergeParallelPatches(NewState(), []BranchPatch{
		{
			NodeID: "left",
			Order:  1,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("answer"), Value: "left"}),
		},
		{
			NodeID: "right",
			Order:  2,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("answer"), Value: "right"}),
		},
	}, ParallelMergeOptions{})
	if err == nil {
		t.Fatal("expected conflict")
	}
	if !strings.Contains(err.Error(), `parallel state merge conflict at "shared.answer"`) {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestMergeParallelPatchesAllowsIdenticalSetWrites(t *testing.T) {
	t.Parallel()

	merged, err := MergeParallelPatches(NewState(), []BranchPatch{
		{
			NodeID: "left",
			Order:  1,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("status"), Value: "ready"}),
		},
		{
			NodeID: "right",
			Order:  2,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("status"), Value: "ready"}),
		},
	}, ParallelMergeOptions{})
	if err != nil {
		t.Fatalf("merge parallel patches: %v", err)
	}

	status, ok := NewAccess(nil, merged).ReadAny(Shared("status"))
	if !ok || status != "ready" {
		t.Fatalf("expected ready status, got %#v ok=%v", status, ok)
	}
}

func TestMergeParallelPatchesRejectsConflictingMergeKeys(t *testing.T) {
	t.Parallel()

	_, err := MergeParallelPatches(NewState(), []BranchPatch{
		{
			NodeID: "left",
			Order:  1,
			Patch: NewPatch(PatchOp{
				Kind:  OpMerge,
				Path:  Shared("meta"),
				Value: map[string]any{"score": 1},
			}),
		},
		{
			NodeID: "right",
			Order:  2,
			Patch: NewPatch(PatchOp{
				Kind:  OpMerge,
				Path:  Shared("meta"),
				Value: map[string]any{"score": 2},
			}),
		},
	}, ParallelMergeOptions{})
	if err == nil {
		t.Fatal("expected conflict")
	}
	if !strings.Contains(err.Error(), "conflicting merge values at score") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestMergeParallelPatchesRejectsParentChildOverlap(t *testing.T) {
	t.Parallel()

	_, err := MergeParallelPatches(NewState(), []BranchPatch{
		{
			NodeID: "parent",
			Order:  1,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("profile"), Value: map[string]any{"name": "a"}}),
		},
		{
			NodeID: "child",
			Order:  2,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("profile", "name"), Value: "b"}),
		},
	}, ParallelMergeOptions{})
	if err == nil {
		t.Fatal("expected conflict")
	}
	if !strings.Contains(err.Error(), "overlapping parent/child writes") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestMergeParallelPatchesValidatesBranchContracts(t *testing.T) {
	t.Parallel()

	_, err := MergeParallelPatches(NewState(), []BranchPatch{
		{
			NodeID: "worker",
			Order:  1,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("private"), Value: "nope"}),
		},
	}, ParallelMergeOptions{
		Contracts: map[string]Contract{
			"worker": NewContract(NewRef[string](Shared("public")).WriteField()),
		},
	})
	if err == nil {
		t.Fatal("expected contract violation")
	}
	if !strings.Contains(err.Error(), `writes undeclared path "shared.private"`) {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestMergeParallelPatchesRejectsAppendStrategyWithSetOps(t *testing.T) {
	t.Parallel()

	itemsRef := NewRef[[]string](Shared("items")).WithMerge(MergeAppend)
	_, err := MergeParallelPatches(NewState(), []BranchPatch{
		{
			NodeID: "left",
			Order:  1,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("items"), Value: []string{"left"}}),
		},
		{
			NodeID: "right",
			Order:  2,
			Patch:  NewPatch(PatchOp{Kind: OpSet, Path: Shared("items"), Value: []string{"right"}}),
		},
	}, ParallelMergeOptions{
		Contracts: map[string]Contract{
			"left":  NewContract(itemsRef.WriteField()),
			"right": NewContract(itemsRef.WriteField()),
		},
	})
	if err == nil {
		t.Fatal("expected conflict")
	}
	if !strings.Contains(err.Error(), "append merge strategy requires append ops") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestMergeParallelPatchesRejectsIncompatibleMergeStrategies(t *testing.T) {
	t.Parallel()

	appendRef := NewRef[[]string](Shared("items")).WithMerge(MergeAppend)
	replaceRef := NewRef[[]string](Shared("items")).WithMerge(MergeReplace)
	_, err := MergeParallelPatches(NewState(), []BranchPatch{
		{
			NodeID: "left",
			Order:  1,
			Patch:  NewPatch(PatchOp{Kind: OpAppend, Path: Shared("items"), Value: "left"}),
		},
		{
			NodeID: "right",
			Order:  2,
			Patch:  NewPatch(PatchOp{Kind: OpAppend, Path: Shared("items"), Value: "right"}),
		},
	}, ParallelMergeOptions{
		Contracts: map[string]Contract{
			"left":  NewContract(appendRef.WriteField()),
			"right": NewContract(replaceRef.WriteField()),
		},
	})
	if err == nil {
		t.Fatal("expected conflict")
	}
	if !strings.Contains(err.Error(), "incompatible merge strategies") {
		t.Fatalf("unexpected error %v", err)
	}
}
