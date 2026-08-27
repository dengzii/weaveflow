package state

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSnapshotRoundTripPreservesMetadataAndArtifacts(t *testing.T) {
	current := FromShared(map[string]any{
		"request": map[string]any{"input": "hello"},
	})
	runtimeState := RuntimeState{
		RunID:          "run-1",
		CurrentStepID:  "step-1",
		CurrentTaskID:  "task-1",
		CurrentNodeIDs: []string{"planner", "worker"},
		NextNodeIDs:    []string{"reviewer"},
		Status:         "paused",
		RetryCount:     2,
		PauseRequested: true,
		BreakpointHit: &BreakpointHit{
			BreakpointID: "breakpoint-1",
			NodeID:       "worker",
			Stage:        "after",
			HitAt:        time.Date(2026, 8, 26, 1, 2, 3, 0, time.UTC),
		},
	}
	artifacts := []ArtifactRef{{
		ID:      "artifact-1",
		RunID:   "run-1",
		RunPath: []string{"run-1", "child-1"},
		Type:    "report",
	}}

	snapshot, err := SnapshotFromStateWithRuntime(current, runtimeState, artifacts)
	if err != nil {
		t.Fatalf("SnapshotFromStateWithRuntime() error = %v", err)
	}
	artifacts[0].RunPath[0] = "mutated"

	restored, err := RestoreStateSnapshot(snapshot)
	if err != nil {
		t.Fatalf("RestoreStateSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(restored.Runtime, runtimeState) {
		t.Fatalf("runtime = %#v, want %#v", restored.Runtime, runtimeState)
	}
	if len(restored.Artifacts) != 1 || restored.Artifacts[0].RunPath[0] != "run-1" {
		t.Fatalf("artifacts = %#v", restored.Artifacts)
	}
	if value, ok := ReadPath(restored.Business, "shared.request.input"); !ok || value != "hello" {
		t.Fatalf("restored request input = %#v, found = %v", value, ok)
	}

	restored.Artifacts[0].RunPath[0] = "changed"
	restoredAgain, err := RestoreStateSnapshot(snapshot)
	if err != nil {
		t.Fatalf("second RestoreStateSnapshot() error = %v", err)
	}
	if restoredAgain.Artifacts[0].RunPath[0] != "run-1" {
		t.Fatalf("snapshot artifacts were aliased: %#v", restoredAgain.Artifacts)
	}
}

func TestRestoreStateSnapshotRejectsMalformedArtifacts(t *testing.T) {
	_, err := RestoreStateSnapshot(Snapshot{
		Version: DefaultSnapshotVersion,
		Runtime: map[string]any{runtimeArtifactsKey: "not-an-artifact-list"},
	})
	if err == nil || !strings.Contains(err.Error(), "decode runtime artifacts") {
		t.Fatalf("RestoreStateSnapshot() error = %v", err)
	}
}

func TestMergeResumeInputOverlaysBusinessStateWithoutAliasing(t *testing.T) {
	base := FromShared(map[string]any{
		"profile": map[string]any{"name": "old", "keep": true},
		"base":    1,
	})
	input := FromShared(map[string]any{
		"profile":  map[string]any{"name": "new"},
		"incoming": 2,
	})

	merged, err := MergeResumeInput(base, input)
	if err != nil {
		t.Fatalf("MergeResumeInput() error = %v", err)
	}
	for path, want := range map[string]any{
		"shared.profile.name": "new",
		"shared.profile.keep": true,
		"shared.base":         1,
		"shared.incoming":     2,
	} {
		if got, ok := ReadPath(merged, path); !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadPath(%q) = %#v, %v; want %#v", path, got, ok, want)
		}
	}
	if got, _ := ReadPath(base, "shared.profile.name"); got != "old" {
		t.Fatalf("base state was mutated: %#v", got)
	}
	if err := SetPath(input, "shared.profile.name", "later"); err != nil {
		t.Fatalf("mutate input: %v", err)
	}
	if got, _ := ReadPath(merged, "shared.profile.name"); got != "new" {
		t.Fatalf("merged state aliases input: %#v", got)
	}

	empty, err := PrepareContinuationState(nil, nil)
	if err != nil || empty == nil || CountKeys(empty) != 0 || CountScopes(empty) != 0 {
		t.Fatalf("PrepareContinuationState(nil, nil) = %#v, %v", empty, err)
	}
	baseOnly, err := MergeResumeInput(base, nil)
	if err != nil {
		t.Fatalf("MergeResumeInput(base, nil) error = %v", err)
	}
	if err := SetPath(baseOnly, "shared.base", 9); err != nil {
		t.Fatalf("mutate base clone: %v", err)
	}
	if got, _ := ReadPath(base, "shared.base"); got != 1 {
		t.Fatalf("base-only result aliases base: %#v", got)
	}
}

func TestRuntimeStatePathHelpersMutateAndCloneValues(t *testing.T) {
	current := NewState()
	if err := SetPath(current, "shared.config", map[string]any{"enabled": true}); err != nil {
		t.Fatalf("SetPath() error = %v", err)
	}
	if err := MergePath(current, "shared.config", map[string]any{"retries": 3}); err != nil {
		t.Fatalf("MergePath() error = %v", err)
	}
	resolved, ok := ResolveStateValue(current.Export(), []string{"shared", "config"})
	if !ok {
		t.Fatal("ResolveStateValue() did not find config")
	}
	resolvedMap, ok := resolved.(map[string]any)
	if !ok || resolvedMap["enabled"] != true || resolvedMap["retries"] != 3 {
		t.Fatalf("resolved config = %#v", resolved)
	}
	resolvedMap["enabled"] = false
	if got, _ := ReadPath(current, "shared.config.enabled"); got != true {
		t.Fatalf("ResolveStateValue() returned aliased value: %#v", got)
	}

	if err := DeletePath(current, "shared.config.retries"); err != nil {
		t.Fatalf("DeletePath() error = %v", err)
	}
	if _, ok := ReadPath(current, "shared.config.retries"); ok {
		t.Fatal("deleted value is still present")
	}
	if _, ok := ResolveStateValue(current.Export(), []string{"shared", "", "config"}); ok {
		t.Fatal("ResolveStateValue() accepted an empty segment")
	}
	if _, ok := ResolveStateValue([]any{"not", "a", "map"}, []string{"shared"}); ok {
		t.Fatal("ResolveStateValue() traversed a non-map")
	}
	if got := SplitStatePath(" shared . request.. input "); !reflect.DeepEqual(got, []string{"shared", "request", "input"}) {
		t.Fatalf("SplitStatePath() = %#v", got)
	}
	for _, operation := range []func() error{
		func() error { return SetPath(current, "unknown.value", 1) },
		func() error { return DeletePath(current, "shared.bad.segment.with.dot.") },
		func() error { return MergePath(current, "unknown.value", map[string]any{}) },
	} {
		if err := operation(); err == nil {
			t.Fatal("invalid state path was accepted")
		}
	}
}

func TestRuntimeStateSummaryAndArtifactCloning(t *testing.T) {
	current := FromMap(map[string]any{
		SectionShared: map[string]any{"one": 1, "two": 2},
		SectionScopes: map[string]any{"worker": map[string]any{"value": 3}},
	})
	fields := SummaryFields(current)
	if len(fields) != 2 || fields[0].Key != "state_keys" || fields[0].Integer != 2 || fields[1].Key != "state_scopes" || fields[1].Integer != 1 {
		t.Fatalf("SummaryFields() = %#v", fields)
	}
	if CountKeys(nil) != 0 || CountScopes(nil) != 0 {
		t.Fatal("nil state summary should be empty")
	}

	original := []ArtifactRef{{ID: "artifact", RunPath: []string{"root", "child"}}}
	cloned := CloneArtifactRefs(original)
	cloned[0].RunPath[0] = "changed"
	if original[0].RunPath[0] != "root" {
		t.Fatalf("CloneArtifactRefs() aliased RunPath: %#v", original)
	}
	if CloneArtifactRefs(nil) != nil {
		t.Fatal("CloneArtifactRefs(nil) should return nil")
	}
}
