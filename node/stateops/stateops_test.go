package stateops

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestStateSetNodeWritesJSONValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value any
		want  any
	}{
		{name: "null", value: nil, want: nil},
		{name: "scalar", value: "ready", want: "ready"},
		{name: "array", value: []any{1, "two"}, want: []any{float64(1), "two"}},
		{name: "object", value: map[string]any{"count": 2}, want: map[string]any{"count": float64(2)}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			node := NewStateSetNode(test.value, core.WithID("set"))
			node.TargetPath = mustPath(t, "shared.result")
			result := executeNode(t, state.NewState(), node)
			assertSinglePatch(t, result.Patch, state.OpSet, "shared.result", test.want)
			got, ok := state.ReadPath(result.State, "shared.result")
			if !ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("result = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestStateSetNodeRejectsNonJSONValueWithNodeID(t *testing.T) {
	t.Parallel()
	node := NewStateSetNode(make(chan int), core.WithID("invalid_set"))
	node.TargetPath = mustPath(t, "shared.result")
	_, err := core.ExecuteNode(context.Background(), state.NewState(), node)
	if err == nil || !strings.Contains(err.Error(), "invalid_set") || !strings.Contains(err.Error(), "JSON compatible") {
		t.Fatalf("ExecuteNode() error = %v", err)
	}
}

func TestStateCopyNodeSupportsSamePath(t *testing.T) {
	t.Parallel()
	path := mustPath(t, "shared.value")
	node := NewStateCopyNode(core.WithID("copy"))
	node.SourcePath = path
	node.TargetPath = path
	result := executeNode(t, state.FromShared(map[string]any{"value": map[string]any{"nested": []any{"a"}}}), node)
	assertSinglePatch(t, result.Patch, state.OpSet, path.String(), map[string]any{"nested": []any{"a"}})
	contract := node.Contract()
	if len(contract.Fields) != 1 || contract.Fields[0].Mode != state.AccessReadWrite || !contract.Fields[0].Required {
		t.Fatalf("contract = %#v", contract)
	}
}

func TestStateDeleteNodeIsIdempotent(t *testing.T) {
	t.Parallel()
	node := NewStateDeleteNode(core.WithID("delete"))
	node.TargetPath = mustPath(t, "shared.missing")
	result := executeNode(t, state.NewState(), node)
	assertSinglePatch(t, result.Patch, state.OpDelete, "shared.missing", nil)
}

func TestStateMergeNodeUsesStateMergeSemantics(t *testing.T) {
	t.Parallel()
	node := NewStateMergeNode(core.WithID("merge"))
	node.SourcePath = mustPath(t, "shared.overlay")
	node.TargetPath = mustPath(t, "shared.target")
	result := executeNode(t, state.FromShared(map[string]any{
		"overlay": map[string]any{"nested": map[string]any{"right": 2}, "added": true},
		"target":  map[string]any{"nested": map[string]any{"left": 1}},
	}), node)
	assertSinglePatch(t, result.Patch, state.OpMerge, "shared.target", map[string]any{
		"nested": map[string]any{"right": float64(2)}, "added": true,
	})
	got, _ := state.ReadPath(result.State, "shared.target")
	want := map[string]any{
		"nested": map[string]any{"left": 1, "right": float64(2)},
		"added":  true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged result = %#v, want %#v", got, want)
	}
}

func TestStateMergeNodeRejectsInvalidSourceAndTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		initial map[string]any
		want    string
	}{
		{name: "source", initial: map[string]any{"overlay": "invalid", "target": map[string]any{}}, want: "requires a JSON object"},
		{name: "target", initial: map[string]any{"overlay": map[string]any{"ok": true}, "target": "invalid"}, want: "found non-object"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			node := NewStateMergeNode(core.WithID("merge_invalid"))
			node.SourcePath = mustPath(t, "shared.overlay")
			node.TargetPath = mustPath(t, "shared.target")
			_, err := core.ExecuteNode(context.Background(), state.FromShared(test.initial), node)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "merge_invalid") {
				t.Fatalf("ExecuteNode() error = %v", err)
			}
		})
	}
}

func TestStateAppendNodePreservesAppendInputSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		target    any
		source    any
		want      any
		wantPatch any
	}{
		{name: "single", target: []any{"a"}, source: "b", want: []any{"a", "b"}, wantPatch: "b"},
		{name: "array", target: []any{"a"}, source: []any{"b", "c"}, want: []any{"a", "b", "c"}, wantPatch: []any{"b", "c"}},
		{name: "empty_array", target: []any{"a"}, source: []any{}, want: []any{"a"}, wantPatch: []any{}},
		{name: "null", source: nil, want: []any{nil}, wantPatch: nil},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			initial := map[string]any{"source": test.source}
			if test.target != nil {
				initial["target"] = test.target
			}
			node := NewStateAppendNode(core.WithID("append"))
			node.SourcePath = mustPath(t, "shared.source")
			node.TargetPath = mustPath(t, "shared.target")
			result := executeNode(t, state.FromShared(initial), node)
			assertSinglePatch(t, result.Patch, state.OpAppend, "shared.target", test.wantPatch)
			got, _ := state.ReadPath(result.State, "shared.target")
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("append result = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestStateTransformNodeFiltersMapsAndProjects(t *testing.T) {
	t.Parallel()
	node, err := NewStateTransformNode(
		"inputs.items.filter(item, item.enabled).map(item, {'id': item.id, 'score': item.score * 2.0})",
		core.WithID("transform"),
	)
	if err != nil {
		t.Fatalf("NewStateTransformNode(): %v", err)
	}
	node.InputPaths = map[string]state.Path{"items": mustPath(t, "shared.items")}
	node.OutputPath = mustPath(t, "shared.result")
	result := executeNode(t, state.FromShared(map[string]any{
		"items": []any{
			map[string]any{"id": "a", "score": 1, "enabled": false},
			map[string]any{"id": "b", "score": 2, "enabled": true},
		},
	}), node)
	want := []any{map[string]any{"id": "b", "score": float64(4)}}
	assertSinglePatch(t, result.Patch, state.OpSet, "shared.result", want)
}

func TestStateTransformNodeCombinesDynamicInputs(t *testing.T) {
	t.Parallel()
	node, err := NewStateTransformNode(
		"{'total': inputs.price * inputs.quantity, 'eligible': inputs.vip && inputs.price * inputs.quantity >= 100}",
		core.WithID("transform_multi"),
	)
	if err != nil {
		t.Fatalf("NewStateTransformNode(): %v", err)
	}
	node.InputPaths = map[string]state.Path{
		"price": mustPath(t, "shared.cart.price"), "quantity": mustPath(t, "shared.cart.quantity"), "vip": mustPath(t, "shared.user.vip"),
	}
	node.OutputPath = mustPath(t, "shared.order.result")
	result := executeNode(t, state.FromShared(map[string]any{
		"cart": map[string]any{"price": 25, "quantity": 4}, "user": map[string]any{"vip": true},
	}), node)
	assertSinglePatch(t, result.Patch, state.OpSet, "shared.order.result", map[string]any{"total": float64(100), "eligible": true})
	if got := node.GraphNodeSpec().State; len(got) != 4 || got["price"].Path != "shared.cart.price" || got["output"].Path != "shared.order.result" {
		t.Fatalf("GraphNodeSpec().State = %#v", got)
	}
}

func TestStateTransformNodeSupportsSameInputAndOutputPath(t *testing.T) {
	t.Parallel()
	node, err := NewStateTransformNode("{'count': inputs.value.count + 1.0}", core.WithID("transform_same"))
	if err != nil {
		t.Fatalf("NewStateTransformNode(): %v", err)
	}
	node.InputPaths = map[string]state.Path{"value": mustPath(t, "shared.value")}
	node.OutputPath = node.InputPaths["value"]
	result := executeNode(t, state.FromShared(map[string]any{"value": map[string]any{"count": 1}}), node)
	assertSinglePatch(t, result.Patch, state.OpSet, "shared.value", map[string]any{"count": float64(2)})
	contract := node.Contract()
	if len(contract.Fields) != 1 || contract.Fields[0].Mode != state.AccessReadWrite {
		t.Fatalf("contract = %#v", contract)
	}
}

func TestStateTransformNodeHandlesMissingAndNullDeterministically(t *testing.T) {
	t.Parallel()
	node, err := NewStateTransformNode(
		"{'name': inputs.value.?name.orValue('unknown'), 'value': inputs.value.?value.orValue(null)}",
		core.WithID("transform_optional"),
	)
	if err != nil {
		t.Fatalf("NewStateTransformNode(): %v", err)
	}
	node.InputPaths = map[string]state.Path{"value": mustPath(t, "shared.input")}
	node.OutputPath = mustPath(t, "shared.output")
	result := executeNode(t, state.FromShared(map[string]any{"input": map[string]any{"value": nil}}), node)
	assertSinglePatch(t, result.Patch, state.OpSet, "shared.output", map[string]any{"name": "unknown", "value": nil})
}

func TestStateTransformDefinitionCompilesExpressionAtBuildTime(t *testing.T) {
	t.Parallel()
	definition := StateTransformNodeTypeDefinition()
	resolved := resolvedNodeSpec(t, NodeTypeStateTransform, map[string]any{"expression": "unbound.value"}, map[string]string{
		"value": "shared.input", "output": "shared.output",
	})
	_, err := definition.Build(&registry.BuildContext{}, resolved)
	if err == nil || !strings.Contains(err.Error(), "state_transform") || !strings.Contains(err.Error(), "unbound") {
		t.Fatalf("Build() error = %v", err)
	}
}

func TestStateTransformNodeEnforcesSizeLimitsAndJSONOutput(t *testing.T) {
	t.Parallel()
	t.Run("input", func(t *testing.T) {
		node, err := NewStateTransformNode("inputs.value", core.WithID("large_input"))
		if err != nil {
			t.Fatalf("NewStateTransformNode(): %v", err)
		}
		node.InputPaths = map[string]state.Path{"value": mustPath(t, "shared.input")}
		node.OutputPath = mustPath(t, "shared.output")
		_, err = core.ExecuteNode(context.Background(), state.FromShared(map[string]any{"input": strings.Repeat("x", maxTransformInputBytes+1)}), node)
		if err == nil || !strings.Contains(err.Error(), "input exceeds") {
			t.Fatalf("ExecuteNode() error = %v", err)
		}
	})
	t.Run("output", func(t *testing.T) {
		node, err := NewStateTransformNode("inputs.value + inputs.value", core.WithID("large_output"))
		if err != nil {
			t.Fatalf("NewStateTransformNode(): %v", err)
		}
		node.InputPaths = map[string]state.Path{"value": mustPath(t, "shared.input")}
		node.OutputPath = mustPath(t, "shared.output")
		_, err = core.ExecuteNode(context.Background(), state.FromShared(map[string]any{"input": strings.Repeat("x", maxTransformOutputBytes/2+1)}), node)
		if err == nil || !strings.Contains(err.Error(), "output exceeds") {
			t.Fatalf("ExecuteNode() error = %v", err)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		node, err := NewStateTransformNode("b'abc'", core.WithID("bytes_output"))
		if err != nil {
			t.Fatalf("NewStateTransformNode(): %v", err)
		}
		node.InputPaths = map[string]state.Path{"value": mustPath(t, "shared.input")}
		node.OutputPath = mustPath(t, "shared.output")
		_, err = core.ExecuteNode(context.Background(), state.FromShared(map[string]any{"input": true}), node)
		if err == nil || !strings.Contains(err.Error(), "not JSON compatible") {
			t.Fatalf("ExecuteNode() error = %v", err)
		}
	})
}

func TestStateTransformNodeEnforcesRuntimeCostLimit(t *testing.T) {
	t.Parallel()
	node, err := NewStateTransformNode("inputs.value.map(item, item)", core.WithID("cost_limit"))
	if err != nil {
		t.Fatalf("NewStateTransformNode(): %v", err)
	}
	node.InputPaths = map[string]state.Path{"value": mustPath(t, "shared.input")}
	node.OutputPath = mustPath(t, "shared.output")
	input := make([]any, 120_000)
	for index := range input {
		input[index] = 0
	}
	_, err = core.ExecuteNode(context.Background(), state.FromShared(map[string]any{"input": input}), node)
	if err == nil || !strings.Contains(err.Error(), "cost limit") {
		t.Fatalf("ExecuteNode() error = %v", err)
	}
}

func TestStateOperationDefinitionsDeclareStrictPorts(t *testing.T) {
	t.Parallel()
	wantPorts := map[string][]struct {
		name     string
		mode     dsl.StateAccessMode
		merge    dsl.StateMergeStrategy
		required bool
	}{
		NodeTypeStateSet:       {{"target", dsl.StateAccessWrite, dsl.StateMergeReplace, true}},
		NodeTypeStateCopy:      {{"source", dsl.StateAccessRead, dsl.StateMergeReplace, true}, {"target", dsl.StateAccessWrite, dsl.StateMergeReplace, true}},
		NodeTypeStateDelete:    {{"target", dsl.StateAccessWrite, dsl.StateMergeReplace, true}},
		NodeTypeStateMerge:     {{"source", dsl.StateAccessRead, dsl.StateMergeReplace, true}, {"target", dsl.StateAccessWrite, dsl.StateMergeMerge, true}},
		NodeTypeStateAppend:    {{"source", dsl.StateAccessRead, dsl.StateMergeReplace, true}, {"target", dsl.StateAccessWrite, dsl.StateMergeAppend, true}},
		NodeTypeStateTransform: {{"output", dsl.StateAccessWrite, dsl.StateMergeReplace, true}},
	}
	for _, definition := range NodeTypeDefinitions() {
		ports := wantPorts[definition.Type]
		if len(definition.StatePorts) != len(ports) {
			t.Fatalf("%s ports = %#v", definition.Type, definition.StatePorts)
		}
		for index, want := range ports {
			got := definition.StatePorts[index]
			if got.Name != want.name || got.Mode != want.mode || got.MergeStrategy != want.merge || got.Required != want.required || got.DefaultPath != "" {
				t.Fatalf("%s port %d = %#v, want %#v", definition.Type, index, got, want)
			}
		}
		if definition.Type == NodeTypeStateTransform {
			dynamic := definition.DynamicStatePorts
			if dynamic == nil || !dynamic.Required || dynamic.Mode != dsl.StateAccessRead || dynamic.MergeStrategy != dsl.StateMergeReplace {
				t.Fatalf("state_transform dynamic ports = %#v", dynamic)
			}
		}
		if definition.ConfigSchema["additionalProperties"] != false {
			t.Fatalf("%s config schema is not strict: %#v", definition.Type, definition.ConfigSchema)
		}
	}
}

func TestStateSetGraphNodeSpecPreservesExplicitNull(t *testing.T) {
	t.Parallel()
	node := NewStateSetNode(nil, core.WithID("set_null"))
	node.TargetPath = mustPath(t, "shared.value")
	raw, err := json.Marshal(node.GraphNodeSpec())
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	var roundTrip dsl.GraphNodeSpec
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("Unmarshal(): %v", err)
	}
	value, exists := roundTrip.Config["value"]
	if !exists || value != nil || roundTrip.State["target"].Path != "shared.value" {
		t.Fatalf("round trip = %#v", roundTrip)
	}
}

func TestStateOperationBuildersRejectUnknownConfig(t *testing.T) {
	t.Parallel()
	definition := StateCopyNodeTypeDefinition()
	resolved := resolvedNodeSpec(t, NodeTypeStateCopy, map[string]any{"source_path": "shared.hidden"}, map[string]string{
		"source": "shared.source", "target": "shared.target",
	})
	_, err := definition.Build(&registry.BuildContext{}, resolved)
	if err == nil || !strings.Contains(err.Error(), "source_path") {
		t.Fatalf("Build() error = %v", err)
	}
}

func executeNode(t *testing.T, initial *state.State, node core.Node) core.ExecutionResult {
	t.Helper()
	result, err := core.ExecuteNode(context.Background(), initial, node)
	if err != nil {
		t.Fatalf("ExecuteNode(): %v", err)
	}
	return result
}

func assertSinglePatch(t *testing.T, patch state.Patch, kind state.PatchOpKind, path string, value any) {
	t.Helper()
	ops := patch.Ops()
	if len(ops) != 1 {
		t.Fatalf("patch ops = %#v", ops)
	}
	if ops[0].Kind != kind || ops[0].Path.String() != path || !reflect.DeepEqual(ops[0].Value, value) {
		t.Fatalf("patch op = %#v, want kind=%q path=%q value=%#v", ops[0], kind, path, value)
	}
}

func resolvedNodeSpec(t *testing.T, nodeType string, config map[string]any, paths map[string]string) registry.ResolvedNodeSpec {
	t.Helper()
	bindings := make(map[string]registry.ResolvedStateBinding, len(paths))
	stateBindings := make(map[string]dsl.StateBinding, len(paths))
	for name, pathText := range paths {
		path := mustPath(t, pathText)
		bindings[name] = registry.ResolvedStateBinding{Path: path}
		stateBindings[name] = dsl.StateBinding{Path: path.String()}
	}
	return registry.ResolvedNodeSpec{
		Spec:  dsl.GraphNodeSpec{ID: nodeType + "_node", Type: nodeType, Config: config, State: stateBindings},
		State: bindings,
	}
}

func mustPath(t *testing.T, pathText string) state.Path {
	t.Helper()
	path, err := state.ParsePath(pathText)
	if err != nil {
		t.Fatalf("ParsePath(%q): %v", pathText, err)
	}
	return path
}
