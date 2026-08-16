package state

import (
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

type cloneCycleNode struct {
	Name string
	Next *cloneCycleNode
}

type cloneOpaqueValue struct {
	values []string
}

type cloneOpaqueScalarValue struct {
	name string
}

func TestCloneValuePreservesCyclesAndAliases(t *testing.T) {
	cyclicMap := map[string]any{"name": "source"}
	cyclicMap["self"] = cyclicMap
	cyclicSlice := make([]any, 1)
	cyclicSlice[0] = cyclicSlice
	cyclicNode := &cloneCycleNode{Name: "source"}
	cyclicNode.Next = cyclicNode

	clonedValue, err := CloneValue(map[string]any{
		"left":  cyclicMap,
		"right": cyclicMap,
		"slice": cyclicSlice,
		"node":  cyclicNode,
	})
	if err != nil {
		t.Fatalf("CloneValue() error = %v", err)
	}
	cloned := clonedValue.(map[string]any)
	left := cloned["left"].(map[string]any)
	right := cloned["right"].(map[string]any)
	if reflect.ValueOf(left).Pointer() != reflect.ValueOf(right).Pointer() {
		t.Fatal("CloneValue() did not preserve repeated map identity")
	}
	if reflect.ValueOf(left).Pointer() != reflect.ValueOf(left["self"]).Pointer() {
		t.Fatal("CloneValue() did not preserve map cycle")
	}
	clonedSlice := cloned["slice"].([]any)
	if reflect.ValueOf(clonedSlice).Pointer() != reflect.ValueOf(clonedSlice[0]).Pointer() {
		t.Fatal("CloneValue() did not preserve slice cycle")
	}
	clonedNode := cloned["node"].(*cloneCycleNode)
	if clonedNode == cyclicNode || clonedNode.Next != clonedNode {
		t.Fatalf("CloneValue() pointer cycle = %#v", clonedNode)
	}
	left["name"] = "cloned"
	clonedSlice[0] = "cloned"
	clonedNode.Name = "cloned"
	sourceSlice := cyclicSlice[0].([]any)
	if cyclicMap["name"] != "source" || reflect.ValueOf(sourceSlice).Pointer() != reflect.ValueOf(cyclicSlice).Pointer() || cyclicNode.Name != "source" {
		t.Fatal("CloneValue() retained a mutable alias to the source graph")
	}
}

func TestStateCloneDoesNotRecurseForeverOnCycles(t *testing.T) {
	cyclicMap := map[string]any{}
	cyclicMap["self"] = cyclicMap
	cyclicNode := &cloneCycleNode{}
	cyclicNode.Next = cyclicNode

	exported := FromShared(map[string]any{
		"map":  cyclicMap,
		"node": cyclicNode,
	}).Export()
	shared := exported[SectionShared].(map[string]any)
	clonedMap := shared["map"].(map[string]any)
	if reflect.ValueOf(clonedMap).Pointer() != reflect.ValueOf(clonedMap["self"]).Pointer() {
		t.Fatal("State.Export() did not preserve map cycle")
	}
	clonedNode := shared["node"].(*cloneCycleNode)
	if clonedNode.Next != clonedNode {
		t.Fatal("State.Export() did not preserve pointer cycle")
	}
}

func TestCloneValueReportsOpaqueMutableReferences(t *testing.T) {
	source := &cloneOpaqueValue{values: []string{"source"}}
	clonedValue, err := CloneValue(map[string]any{"opaque": source})
	if err == nil || !strings.Contains(err.Error(), "cloneOpaqueValue") {
		t.Fatalf("CloneValue() error = %v, want opaque type", err)
	}
	if clonedValue == nil {
		t.Fatal("CloneValue() did not return its documented best-effort copy")
	}
	current := FromShared(map[string]any{"opaque": source})
	if _, err := current.CloneStrict(); err == nil || !strings.Contains(err.Error(), "cloneOpaqueValue") {
		t.Fatalf("State.CloneStrict() error = %v, want opaque type", err)
	}
}

func TestCloneValueHandlesKnownValueSemanticTypes(t *testing.T) {
	integer := big.NewInt(123)
	moment := time.Date(2026, time.August, 16, 12, 30, 0, 0, time.FixedZone("test", 8*60*60))
	opaqueScalar := &cloneOpaqueScalarValue{name: "source"}
	clonedValue, err := CloneValue(map[string]any{
		"integer":       integer,
		"moment":        moment,
		"opaque_scalar": opaqueScalar,
	})
	if err != nil {
		t.Fatalf("CloneValue() error = %v", err)
	}
	cloned := clonedValue.(map[string]any)
	clonedInteger := cloned["integer"].(*big.Int)
	if clonedInteger == integer || clonedInteger.Cmp(integer) != 0 {
		t.Fatalf("CloneValue() integer = %v", clonedInteger)
	}
	clonedInteger.SetInt64(999)
	if integer.Int64() != 123 {
		t.Fatalf("CloneValue() retained big.Int alias: %v", integer)
	}
	if cloned["moment"].(time.Time) != moment {
		t.Fatalf("CloneValue() time = %v, want %v", cloned["moment"], moment)
	}
	clonedOpaqueScalar := cloned["opaque_scalar"].(*cloneOpaqueScalarValue)
	clonedOpaqueScalar.name = "cloned"
	if clonedOpaqueScalar == opaqueScalar || opaqueScalar.name != "source" {
		t.Fatal("CloneValue() retained opaque scalar alias")
	}
}

func TestCloneValueHandlesPatchValuesStrictly(t *testing.T) {
	sourceValue := map[string]any{"items": []any{"source"}}
	patch := NewPatch(PatchOp{Kind: OpSet, Path: Shared("result"), Value: sourceValue})
	current := FromShared(map[string]any{"patch": patch})
	cloned, err := current.CloneStrict()
	if err != nil {
		t.Fatalf("State.CloneStrict() error = %v", err)
	}
	sourceValue["items"].([]any)[0] = "mutated"
	clonedValue, ok := ReadPath(cloned, "shared.patch")
	if !ok {
		t.Fatal("cloned patch is missing")
	}
	clonedPatch, ok := clonedValue.(Patch)
	if !ok {
		t.Fatalf("cloned patch type = %T", clonedValue)
	}
	operations := clonedPatch.Ops()
	items := operations[0].Value.(map[string]any)["items"].([]any)
	if items[0] != "source" {
		t.Fatalf("cloned patch value = %#v", operations[0].Value)
	}

	opaquePatch := NewPatch(PatchOp{Kind: OpSet, Path: Shared("opaque"), Value: &cloneOpaqueValue{values: []string{"opaque"}}})
	if _, err := FromShared(map[string]any{"patch": opaquePatch}).CloneStrict(); err == nil || !strings.Contains(err.Error(), "cloneOpaqueValue") {
		t.Fatalf("State.CloneStrict() opaque patch error = %v", err)
	}
}
