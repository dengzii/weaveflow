package graph

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestGraphV2StateOperationPipeline(t *testing.T) {
	t.Parallel()
	definition := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "state-operations",
		StateModules: protocolModuleRefs(),
		EntryPoint:   "set",
		FinishPoint:  "delete",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "set", Type: node.NodeTypeStateSet,
				Config: map[string]any{"value": map[string]any{"nested": map[string]any{"left": 1}, "value": "base"}},
				State:  map[string]dsl.StateBinding{"target": binding("shared.source")},
			},
			{
				ID: "copy", Type: node.NodeTypeStateCopy,
				State: map[string]dsl.StateBinding{
					"source": binding("shared.source"), "target": binding("shared.working"),
				},
			},
			{
				ID: "merge", Type: node.NodeTypeStateMerge,
				State: map[string]dsl.StateBinding{
					"source": binding("shared.request.metadata"), "target": binding("shared.working"),
				},
			},
			{
				ID: "append", Type: node.NodeTypeStateAppend,
				State: map[string]dsl.StateBinding{
					"source": binding("shared.environment"), "target": binding("shared.list"),
				},
			},
			{
				ID: "delete", Type: node.NodeTypeStateDelete,
				State: map[string]dsl.StateBinding{"target": binding("shared.source")},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "set", To: "copy"},
			{From: "copy", To: "merge"},
			{From: "merge", To: "append"},
			{From: "append", To: "delete"},
		},
	}

	result := runBoundGraph(t, definition, state.FromShared(map[string]any{
		"request":     map[string]any{"metadata": map[string]any{"nested": map[string]any{"right": 2}, "added": true}},
		"environment": map[string]any{"name": "test"},
	}), nil, nil)

	if _, exists := state.ReadPath(result, "shared.source"); exists {
		t.Fatal("shared.source was not deleted")
	}
	working, _ := state.ReadPath(result, "shared.working")
	wantWorking := map[string]any{
		"nested": map[string]any{"left": float64(1), "right": float64(2)},
		"value":  "base",
		"added":  true,
	}
	if !reflect.DeepEqual(working, wantWorking) {
		t.Fatalf("shared.working = %#v, want %#v", working, wantWorking)
	}
	list, _ := state.ReadPath(result, "shared.list")
	if !reflect.DeepEqual(list, []any{map[string]any{"name": "test"}}) {
		t.Fatalf("shared.list = %#v", list)
	}
}

func TestGraphV2DynamicTransformAndStateExpressionCondition(t *testing.T) {
	t.Parallel()
	definition := dynamicStateExpressionDefinition("shared.cart.price")
	result := runBoundGraph(t, definition, state.FromShared(map[string]any{
		"cart": map[string]any{"price": 25, "quantity": 4},
	}), nil, nil)
	total, _ := state.ReadPath(result, "shared.order.total")
	if total != float64(100) {
		t.Fatalf("shared.order.total = %#v", total)
	}
	status, _ := state.ReadPath(result, "shared.order.status")
	if status != "eligible" {
		t.Fatalf("shared.order.status = %#v", status)
	}
}

func TestGraphV2DynamicBindingsChangeSemanticHash(t *testing.T) {
	t.Parallel()
	left, err := NewBuilder(builtin.NewDefaultRegistry()).Build(dynamicStateExpressionDefinition("shared.cart.price"), &registry.BuildContext{})
	if err != nil {
		t.Fatalf("BuildGraph(left): %v", err)
	}
	right, err := NewBuilder(builtin.NewDefaultRegistry()).Build(dynamicStateExpressionDefinition("shared.cart.discounted_price"), &registry.BuildContext{})
	if err != nil {
		t.Fatalf("BuildGraph(right): %v", err)
	}
	leftHash, err := left.SemanticHash()
	if err != nil {
		t.Fatalf("left.SemanticHash(): %v", err)
	}
	rightHash, err := right.SemanticHash()
	if err != nil {
		t.Fatalf("right.SemanticHash(): %v", err)
	}
	if leftHash == rightHash {
		t.Fatalf("dynamic binding path did not change semantic hash: %q", leftHash)
	}
}

func TestGraphV2StateAppendUsesStableParallelOrder(t *testing.T) {
	t.Parallel()
	definition := stateOperationFanOutDefinition(node.NodeTypeStateAppend, "shared.list")
	result := runBoundGraph(t, definition, state.FromShared(map[string]any{
		"request":     map[string]any{"input": "a"},
		"final":       map[string]any{"answer": "b"},
		"environment": map[string]any{},
	}), nil, nil)
	list, _ := state.ReadPath(result, "shared.list")
	if !reflect.DeepEqual(list, []any{"a", "b"}) {
		t.Fatalf("shared.list = %#v", list)
	}
}

func TestGraphV2StateMergeHandlesParallelFieldsAndConflicts(t *testing.T) {
	t.Parallel()
	definition := stateOperationFanOutDefinition(node.NodeTypeStateMerge, "shared.merged")
	t.Run("disjoint", func(t *testing.T) {
		result := runBoundGraph(t, definition, state.FromShared(map[string]any{
			"request":     map[string]any{"input": "start", "metadata": map[string]any{"left": 1}},
			"environment": map[string]any{"right": 2},
		}), nil, nil)
		merged, _ := state.ReadPath(result, "shared.merged")
		if !reflect.DeepEqual(merged, map[string]any{"left": float64(1), "right": float64(2)}) {
			t.Fatalf("shared.merged = %#v", merged)
		}
	})
	t.Run("overlap", func(t *testing.T) {
		workflow, err := NewBuilder(builtin.NewDefaultRegistry()).Build(definition, &registry.BuildContext{})
		if err != nil {
			t.Fatalf("BuildGraph(): %v", err)
		}
		_, err = workflow.Run(context.Background(), state.FromShared(map[string]any{
			"request":     map[string]any{"input": "start", "metadata": map[string]any{"same": 1}},
			"environment": map[string]any{"same": 2},
		}))
		if err == nil || !strings.Contains(err.Error(), "parallel state merge conflict") {
			t.Fatalf("Run() error = %v", err)
		}
	})
}

func TestStateOperationsExampleBuilds(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "examples", "state_operations", "graph.json")
	if _, err := NewBuilder(builtin.NewDefaultRegistry()).BuildFile(path, &registry.BuildContext{}); err != nil {
		t.Fatalf("LoadGraphFromFile(%q): %v", path, err)
	}
}

func stateOperationFanOutDefinition(branchType, targetPath string) dsl.GraphDefinition {
	sourceA := "shared.request.input"
	sourceB := "shared.final.answer"
	if branchType == node.NodeTypeStateMerge {
		sourceA = "shared.request.metadata"
		sourceB = "shared.environment"
	}
	return dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "state-operation-fan-out",
		StateModules: protocolModuleRefs(),
		EntryPoint:   "router",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "router", Type: node.NodeTypeStateCopy,
				State: map[string]dsl.StateBinding{
					"source": binding("shared.request.input"), "target": binding("shared.started"),
				},
			},
			{
				ID: "branch_a", Type: branchType,
				State: map[string]dsl.StateBinding{"source": binding(sourceA), "target": binding(targetPath)},
			},
			{
				ID: "branch_b", Type: branchType,
				State: map[string]dsl.StateBinding{"source": binding(sourceB), "target": binding(targetPath)},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "router", To: "branch_a"},
			{From: "router", To: "branch_b"},
			{From: "branch_a", To: dsl.EndNodeRef},
			{From: "branch_b", To: dsl.EndNodeRef},
		},
	}
}

func dynamicStateExpressionDefinition(pricePath string) dsl.GraphDefinition {
	return dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "dynamic-state-expression",
		StateModules: protocolModuleRefs(),
		EntryPoint:   "set_price",
		FinishPoint:  "eligible",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "set_price", Type: node.NodeTypeStateSet,
				Config: map[string]any{"value": 25},
				State:  map[string]dsl.StateBinding{"target": binding(pricePath)},
			},
			{
				ID: "set_quantity", Type: node.NodeTypeStateSet,
				Config: map[string]any{"value": 4},
				State:  map[string]dsl.StateBinding{"target": binding("shared.cart.quantity")},
			},
			{
				ID: "calculate", Type: node.NodeTypeStateTransform,
				Config: map[string]any{"expression": "inputs.price * inputs.quantity"},
				State: map[string]dsl.StateBinding{
					"price": binding(pricePath), "quantity": binding("shared.cart.quantity"), "output": binding("shared.order.total"),
				},
			},
			{
				ID: "eligible", Type: node.NodeTypeStateSet,
				Config: map[string]any{"value": "eligible"},
				State:  map[string]dsl.StateBinding{"target": binding("shared.order.status")},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "set_price", To: "set_quantity"},
			{From: "set_quantity", To: "calculate"},
			{
				From: "calculate", To: "eligible",
				Condition: &dsl.GraphConditionSpec{
					Type:   builtin.ConditionTypeStateExpression,
					Config: map[string]any{"expression": "inputs.total >= 100"},
					State:  map[string]dsl.StateBinding{"total": binding("shared.order.total")},
				},
			},
			{From: "calculate", To: dsl.EndNodeRef},
		},
	}
}
