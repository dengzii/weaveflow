package nodes

import (
	"context"
	"testing"
	fruntime "weaveflow/runtime"
)

func TestIteratorNodeInjectsCurrentIterationState(t *testing.T) {
	t.Parallel()

	node := NewIteratorNode()
	node.NodeID = "loop"
	node.StateKey = "items"
	node.MaxIterations = 2

	state := fruntime.State{
		"items": []any{"first", "second", "third"},
	}

	next, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke iterator node: %v", err)
	}

	namespace := next.Namespace(IteratorStateNamespace)
	if namespace == nil {
		t.Fatalf("expected iterator namespace")
	}

	rawLoopState := namespace["loop"]
	loopState, ok := rawLoopState.(map[string]any)
	if !ok {
		if typed, ok := rawLoopState.(fruntime.State); ok {
			loopState = typed
		} else {
			t.Fatalf("expected iterator runtime state map, got %#v", rawLoopState)
		}
	}

	if got := loopState["item"]; got != "first" {
		t.Fatalf("expected first item, got %#v", got)
	}
	if got := loopState["index"]; got != 0 {
		t.Fatalf("expected index 0, got %#v", got)
	}
	if got := loopState["iteration"]; got != 1 {
		t.Fatalf("expected iteration 1, got %#v", got)
	}
	if got := loopState["done"]; got != false {
		t.Fatalf("expected done false, got %#v", got)
	}
	if got := loopState["is_last"]; got != false {
		t.Fatalf("expected is_last false, got %#v", got)
	}
}

func TestIteratorNodeMarksDoneWhenExhausted(t *testing.T) {
	t.Parallel()

	node := NewIteratorNode()
	node.NodeID = "loop"
	node.StateKey = "items"
	node.MaxIterations = 1

	state := fruntime.State{
		"items": []string{"first", "second"},
	}

	if _, err := node.Invoke(context.Background(), state); err != nil {
		t.Fatalf("first invoke iterator node: %v", err)
	}
	next, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("second invoke iterator node: %v", err)
	}

	namespace := next.Namespace(IteratorStateNamespace)
	rawLoopState := namespace["loop"]
	loopState, ok := rawLoopState.(map[string]any)
	if !ok {
		if typed, ok := rawLoopState.(fruntime.State); ok {
			loopState = typed
		} else {
			t.Fatalf("expected iterator runtime state map, got %#v", rawLoopState)
		}
	}

	if got := loopState["done"]; got != true {
		t.Fatalf("expected done true, got %#v", loopState)
	}
	if _, exists := loopState["item"]; exists {
		t.Fatalf("expected item cleared after exhaustion, got %#v", loopState)
	}
	if got := loopState["limit"]; got != 1 {
		t.Fatalf("expected limit 1, got %#v", got)
	}
}

func TestIteratorNodeReadsNestedStatePath(t *testing.T) {
	t.Parallel()

	node := NewIteratorNode()
	node.NodeID = "loop"
	node.StateKey = "payload.items.1.values"
	node.MaxIterations = 3

	state := fruntime.State{
		"payload": map[string]any{
			"items": []any{
				map[string]any{"values": []any{"skip"}},
				map[string]any{"values": []string{"alpha", "beta"}},
			},
		},
	}

	next, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke iterator node with nested path: %v", err)
	}

	namespace := next.Namespace(IteratorStateNamespace)
	rawLoopState := namespace["loop"]
	loopState, ok := rawLoopState.(map[string]any)
	if !ok {
		if typed, ok := rawLoopState.(fruntime.State); ok {
			loopState = typed
		} else {
			t.Fatalf("expected iterator runtime state map, got %#v", rawLoopState)
		}
	}

	if got := loopState["item"]; got != "alpha" {
		t.Fatalf("expected nested first item, got %#v", got)
	}
	if got := loopState["total"]; got != 2 {
		t.Fatalf("expected total 2, got %#v", got)
	}
}

func TestIteratorNodeNormalizesStructuredItems(t *testing.T) {
	t.Parallel()

	type item struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	node := NewIteratorNode()
	node.NodeID = "loop"
	node.StateKey = "items"
	node.MaxIterations = 1

	state := fruntime.State{
		"items": []item{{Name: "alpha", Count: 2}},
	}

	next, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke iterator node with structured items: %v", err)
	}

	namespace := next.Namespace(IteratorStateNamespace)
	rawLoopState := namespace["loop"]
	loopState, ok := rawLoopState.(map[string]any)
	if !ok {
		if typed, ok := rawLoopState.(fruntime.State); ok {
			loopState = typed
		} else {
			t.Fatalf("expected iterator runtime state map, got %#v", rawLoopState)
		}
	}

	itemValue, ok := loopState["item"].(map[string]any)
	if !ok {
		if typed, ok := loopState["item"].(fruntime.State); ok {
			itemValue = typed
		} else {
			t.Fatalf("expected normalized iterator item map, got %#v", loopState["item"])
		}
	}
	if got := itemValue["name"]; got != "alpha" {
		t.Fatalf("expected normalized item name alpha, got %#v", got)
	}
	if got := itemValue["count"]; got != float64(2) {
		t.Fatalf("expected normalized item count 2, got %#v", got)
	}
}
