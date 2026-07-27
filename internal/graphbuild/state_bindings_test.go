package graphbuild

import (
	"context"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

const (
	testModuleName      = "test.protocols"
	testModuleVersion   = "1"
	testConversationCap = "test.conversation.v1"
	testAlternativeCap  = "test.alternative.v1"
	testUnreferencedCap = "test.unreferenced.v1"
	testPrimitiveNode   = "primitive"
	testCapabilityNode  = "capability"
	testAlternativeNode = "alternative"
	testMergeNode       = "merge"
	testDefaultNode     = "default"
	testConditionType   = "conversation_ready"
)

func TestResolveGraphBindingsRejectsInvalidBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*dsl.GraphDefinition)
		wantErr string
	}{
		{
			name: "state module is not registered",
			mutate: func(def *dsl.GraphDefinition) {
				def.StateModules = []dsl.StateModuleRef{{Name: "missing.module", Version: "1"}}
			},
			wantErr: `state module "missing.module" version "1" is not registered`,
		},
		{
			name: "state module version does not match",
			mutate: func(def *dsl.GraphDefinition) {
				def.StateModules = []dsl.StateModuleRef{{Name: testModuleName, Version: "2"}}
			},
			wantErr: `state module "test.protocols" version "2" is not registered`,
		},
		{
			name: "required port missing",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes[0].State = map[string]dsl.StateBinding{}
			},
			wantErr: `requires state port "value"`,
		},
		{
			name: "unknown port",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes[0].State["unknown"] = dsl.StateBinding{Path: "shared.other"}
			},
			wantErr: `binds unknown state port "unknown"`,
		},
		{
			name: "unknown section",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes[0].State["value"] = dsl.StateBinding{Path: "request.input"}
			},
			wantErr: `unknown state path section "request"`,
		},
		{
			name: "section root",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes[0].State["value"] = dsl.StateBinding{Path: "shared"}
			},
			wantErr: "cannot bind a state section root",
		},
		{
			name: "reserved section",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes[0].State["value"] = dsl.StateBinding{Path: "runtime.input"}
			},
			wantErr: `cannot bind reserved path "runtime.input"`,
		},
		{
			name: "primitive schema conflict",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes[0].State["value"] = dsl.StateBinding{Path: "shared.count"}
			},
			wantErr: `schema type "string" conflicts with module field "shared.count" type "integer"`,
		},
		{
			name: "capability schema conflicts with exact module field",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes = []dsl.GraphNodeSpec{{
					ID: "cap", Type: testCapabilityNode,
					State: map[string]dsl.StateBinding{"root": {Path: "shared.count"}},
				}}
			},
			wantErr: `conflicts with module field "shared.count" schema`,
		},
		{
			name: "expanded capability field conflicts with exact module field",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes = []dsl.GraphNodeSpec{{
					ID: "cap", Type: testCapabilityNode,
					State: map[string]dsl.StateBinding{"root": {Path: "shared.schema_conflict"}},
				}}
			},
			wantErr: `capability field "shared.schema_conflict.messages" type "array" conflicts with module field "shared.schema_conflict.messages" type "string"`,
		},
		{
			name: "unregistered capability",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes = []dsl.GraphNodeSpec{{
					ID: "cap", Type: "unregistered_capability",
					State: map[string]dsl.StateBinding{"root": {Path: "shared.root"}},
				}}
			},
			wantErr: `capability "missing.capability.v1" is not registered`,
		},
		{
			name: "capability from unreferenced module",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes = []dsl.GraphNodeSpec{{
					ID: "cap", Type: "unreferenced_capability",
					State: map[string]dsl.StateBinding{"root": {Path: "shared.root"}},
				}}
			},
			wantErr: "belongs to an unreferenced state module",
		},
		{
			name: "missing capability field",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes = []dsl.GraphNodeSpec{{
					ID: "cap", Type: "missing_capability_field",
					State: map[string]dsl.StateBinding{"root": {Path: "shared.root"}},
				}}
			},
			wantErr: `has no field "missing"`,
		},
		{
			name: "root capability conflict",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes = []dsl.GraphNodeSpec{
					{ID: "conversation", Type: testCapabilityNode, State: map[string]dsl.StateBinding{"root": {Path: "shared.root"}}},
					{ID: "alternative", Type: testAlternativeNode, State: map[string]dsl.StateBinding{"root": {Path: "shared.root"}}},
				}
			},
			wantErr: "bound to incompatible capabilities",
		},
		{
			name: "primitive type conflict on same path",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes = []dsl.GraphNodeSpec{{
					ID: "merge", Type: testMergeNode,
					State: map[string]dsl.StateBinding{
						"read":  {Path: "shared.custom"},
						"write": {Path: "shared.custom"},
					},
				}}
			},
			wantErr: "incompatible schema types",
		},
		{
			name: "merge strategy conflict on same path",
			mutate: func(def *dsl.GraphDefinition) {
				def.Nodes = []dsl.GraphNodeSpec{{
					ID: "merge", Type: "merge_strategy_conflict",
					State: map[string]dsl.StateBinding{
						"left":  {Path: "shared.custom"},
						"right": {Path: "shared.custom"},
					},
				}}
			},
			wantErr: "incompatible merge strategies",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reg := newBindingTestRegistry(t)
			def := baseBindingDefinition()
			test.mutate(&def)
			_, err := ResolveGraphBindings(def, reg)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ResolveGraphBindings() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestResolveGraphBindingsExpandsDynamicStatePorts(t *testing.T) {
	t.Parallel()
	reg := newBindingTestRegistry(t)
	err := reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "dynamic",
			StatePorts: []dsl.StatePortDefinition{
				primitiveTestPort("output", "object", dsl.StateAccessWrite, dsl.StateMergeReplace, true),
			},
			DynamicStatePorts: &dsl.DynamicStatePortDefinition{
				NamePattern: "[A-Za-z_][A-Za-z0-9_]*", MinPorts: 1,
				Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
			},
		},
		Build: func(_ *registry.BuildContext, spec registry.ResolvedNodeSpec) (core.Node, error) {
			return &bindingTestNode{NodeInfo: core.NodeInfo{NodeID: spec.Spec.ID}}, nil
		},
	})
	if err != nil {
		t.Fatalf("register dynamic node: %v", err)
	}
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: []dsl.StateModuleRef{{Name: testModuleName, Version: testModuleVersion}},
		Nodes: []dsl.GraphNodeSpec{{
			ID: "dynamic", Type: "dynamic",
			State: map[string]dsl.StateBinding{
				"zeta": {Path: "shared.zeta"}, "alpha": {Path: "shared.alpha"}, "output": {Path: "shared.output"},
			},
		}},
	}
	resolved, err := ResolveGraphBindings(def, reg)
	if err != nil {
		t.Fatalf("ResolveGraphBindings(): %v", err)
	}
	node := resolved.Nodes["dynamic"]
	for _, name := range []string{"alpha", "zeta", "output"} {
		if _, ok := node.State[name]; !ok {
			t.Fatalf("resolved state missing %q: %#v", name, node.State)
		}
	}
	if got := resolved.NodeContracts["dynamic"].Fields; len(got) != 3 || got[1].Path.String() != "shared.alpha" || got[2].Path.String() != "shared.zeta" {
		t.Fatalf("dynamic contract fields = %#v", got)
	}
}

func TestResolveGraphBindingsRejectsInvalidDynamicStatePorts(t *testing.T) {
	t.Parallel()
	reg := newBindingTestRegistry(t)
	err := reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "dynamic",
			DynamicStatePorts: &dsl.DynamicStatePortDefinition{
				NamePattern: "[A-Za-z_][A-Za-z0-9_]*", MinPorts: 1, MaxPorts: 1,
				Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
			},
		},
		Build: func(_ *registry.BuildContext, spec registry.ResolvedNodeSpec) (core.Node, error) {
			return &bindingTestNode{NodeInfo: core.NodeInfo{NodeID: spec.Spec.ID}}, nil
		},
	})
	if err != nil {
		t.Fatalf("register dynamic node: %v", err)
	}
	tests := []struct {
		name  string
		state map[string]dsl.StateBinding
		want  string
	}{
		{name: "minimum", state: nil, want: "at least 1"},
		{name: "maximum", state: map[string]dsl.StateBinding{"one": {Path: "shared.one"}, "two": {Path: "shared.two"}}, want: "at most 1"},
		{name: "name", state: map[string]dsl.StateBinding{"not-valid": {Path: "shared.value"}}, want: "unknown state port"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveGraphBindings(dsl.GraphDefinition{
				Version:      dsl.GraphDefinitionVersion,
				StateModules: []dsl.StateModuleRef{{Name: testModuleName, Version: testModuleVersion}},
				Nodes:        []dsl.GraphNodeSpec{{ID: "dynamic", Type: "dynamic", State: test.state}},
			}, reg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestResolveGraphBindingsExpandsCapabilityAndMergesPorts(t *testing.T) {
	t.Parallel()
	reg := newBindingTestRegistry(t)
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: []dsl.StateModuleRef{{Name: testModuleName, Version: testModuleVersion}},
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "cap", Type: testCapabilityNode,
				State: map[string]dsl.StateBinding{"root": {Path: "scopes.agent.thread"}},
			},
			{
				ID: "combined", Type: "combined",
				State: map[string]dsl.StateBinding{
					"reader": {Path: "shared.combined"},
					"writer": {Path: "shared.combined"},
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{{
			From: "cap", To: "combined",
			Condition: &dsl.GraphConditionSpec{
				Type:  testConditionType,
				State: map[string]dsl.StateBinding{"root": {Path: "scopes.agent.thread"}},
			},
		}},
	}

	resolved, err := ResolveGraphBindings(def, reg)
	if err != nil {
		t.Fatalf("ResolveGraphBindings(): %v", err)
	}

	capBinding := resolved.Nodes["cap"].State["root"]
	if got := capBinding.Path.String(); got != "scopes.agent.thread" {
		t.Fatalf("capability root path = %q", got)
	}
	if capBinding.Capability != testConversationCap {
		t.Fatalf("capability id = %q", capBinding.Capability)
	}
	assertContractField(t, capBinding.Contract, "scopes.agent.thread.messages", state.AccessReadWrite, state.MergeAppend)
	assertContractField(t, capBinding.Contract, "scopes.agent.thread.answer", state.AccessWrite, state.MergeReplace)

	conditionBinding := resolved.Conditions[0].State["root"]
	if conditionBinding.Path.String() != capBinding.Path.String() || conditionBinding.Capability != capBinding.Capability {
		t.Fatalf("condition binding = %#v, node binding = %#v", conditionBinding, capBinding)
	}
	assertContractField(t, resolved.ConditionContracts[0], "scopes.agent.thread.messages", state.AccessRead, state.MergeAppend)

	combined := resolved.NodeContracts["combined"]
	if len(combined.Fields) != 1 {
		t.Fatalf("combined contract fields = %#v", combined.Fields)
	}
	assertContractField(t, combined, "shared.combined", state.AccessReadWrite, state.MergeReplace)
}

func TestResolveGraphBindingsUsesDefaultPaths(t *testing.T) {
	t.Parallel()
	reg := newBindingTestRegistry(t)
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: []dsl.StateModuleRef{{Name: testModuleName, Version: testModuleVersion}},
		Nodes:        []dsl.GraphNodeSpec{{ID: "writer.one", Type: testDefaultNode}},
	}

	resolved, err := ResolveGraphBindings(def, reg)
	if err != nil {
		t.Fatalf("ResolveGraphBindings(): %v", err)
	}
	binding := resolved.Nodes["writer.one"].State["value"]
	if got := binding.Path.String(); got != "scopes.writer_one.value" {
		t.Fatalf("default path = %q", got)
	}
	if got := resolved.Nodes["writer.one"].Spec.State["value"].Path; got != "scopes.writer_one.value" {
		t.Fatalf("materialized spec path = %q", got)
	}
}

func baseBindingDefinition() dsl.GraphDefinition {
	return dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: []dsl.StateModuleRef{{Name: testModuleName, Version: testModuleVersion}},
		Nodes: []dsl.GraphNodeSpec{{
			ID: "node", Type: testPrimitiveNode,
			State: map[string]dsl.StateBinding{"value": {Path: "shared.input"}},
		}},
	}
}

func newBindingTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	mustRegisterStateModule(t, reg, dsl.StateModuleDefinition{
		Name: testModuleName, Version: testModuleVersion,
		Fields: []dsl.StateFieldDefinition{
			{Path: "shared.input", Schema: dsl.JSONSchema{"type": "string"}},
			{Path: "shared.count", Schema: dsl.JSONSchema{"type": "integer"}},
			{Path: "shared.schema_conflict.messages", Schema: dsl.JSONSchema{"type": "string"}},
		},
		Capabilities: []dsl.StateCapabilityDefinition{
			{
				ID: testConversationCap, Schema: dsl.JSONSchema{"type": "object"},
				Fields: []dsl.StateCapabilityFieldDefinition{
					{Name: "messages", Schema: dsl.JSONSchema{"type": "array"}, MergeStrategy: dsl.StateMergeAppend},
					{Name: "answer", Schema: dsl.JSONSchema{"type": "string"}, MergeStrategy: dsl.StateMergeReplace},
				},
			},
			{
				ID: testAlternativeCap, Schema: dsl.JSONSchema{"type": "object"},
				Fields: []dsl.StateCapabilityFieldDefinition{{Name: "value", Schema: dsl.JSONSchema{"type": "string"}, MergeStrategy: dsl.StateMergeReplace}},
			},
		},
	})
	mustRegisterStateModule(t, reg, dsl.StateModuleDefinition{
		Name: "test.extra", Version: "1",
		Capabilities: []dsl.StateCapabilityDefinition{{
			ID: testUnreferencedCap, Schema: dsl.JSONSchema{"type": "object"},
			Fields: []dsl.StateCapabilityFieldDefinition{{Name: "value", Schema: dsl.JSONSchema{"type": "string"}, MergeStrategy: dsl.StateMergeReplace}},
		}},
	})

	mustRegisterNodeType(t, reg, testPrimitiveNode, []dsl.StatePortDefinition{
		primitiveTestPort("value", "string", dsl.StateAccessRead, dsl.StateMergeReplace, true),
	})
	mustRegisterNodeType(t, reg, testDefaultNode, []dsl.StatePortDefinition{{
		Name: "value", Required: true, DefaultPath: "scopes.{node_id}.value",
		Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
	}})
	mustRegisterNodeType(t, reg, testCapabilityNode, []dsl.StatePortDefinition{
		capabilityTestPort("root", testConversationCap,
			dsl.RelativeStateFieldRef{Path: "messages", Mode: dsl.StateAccessReadWrite},
			dsl.RelativeStateFieldRef{Path: "answer", Mode: dsl.StateAccessWrite}),
	})
	mustRegisterNodeType(t, reg, testAlternativeNode, []dsl.StatePortDefinition{
		capabilityTestPort("root", testAlternativeCap, dsl.RelativeStateFieldRef{Path: "value", Mode: dsl.StateAccessRead}),
	})
	mustRegisterNodeType(t, reg, "unregistered_capability", []dsl.StatePortDefinition{
		capabilityTestPort("root", "missing.capability.v1", dsl.RelativeStateFieldRef{Path: "value", Mode: dsl.StateAccessRead}),
	})
	mustRegisterNodeType(t, reg, "unreferenced_capability", []dsl.StatePortDefinition{
		capabilityTestPort("root", testUnreferencedCap, dsl.RelativeStateFieldRef{Path: "value", Mode: dsl.StateAccessRead}),
	})
	mustRegisterNodeType(t, reg, "missing_capability_field", []dsl.StatePortDefinition{
		capabilityTestPort("root", testConversationCap, dsl.RelativeStateFieldRef{Path: "missing", Mode: dsl.StateAccessRead}),
	})
	mustRegisterNodeType(t, reg, testMergeNode, []dsl.StatePortDefinition{
		primitiveTestPort("read", "string", dsl.StateAccessRead, dsl.StateMergeReplace, true),
		primitiveTestPort("write", "integer", dsl.StateAccessWrite, dsl.StateMergeReplace, true),
	})
	mustRegisterNodeType(t, reg, "merge_strategy_conflict", []dsl.StatePortDefinition{
		primitiveTestPort("left", "array", dsl.StateAccessWrite, dsl.StateMergeReplace, true),
		primitiveTestPort("right", "array", dsl.StateAccessWrite, dsl.StateMergeAppend, true),
	})
	mustRegisterNodeType(t, reg, "combined", []dsl.StatePortDefinition{
		primitiveTestPort("reader", "string", dsl.StateAccessRead, dsl.StateMergeReplace, true),
		primitiveTestPort("writer", "string", dsl.StateAccessWrite, dsl.StateMergeReplace, true),
	})

	condition := registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type: testConditionType,
			StatePorts: []dsl.StatePortDefinition{
				capabilityTestPort("root", testConversationCap, dsl.RelativeStateFieldRef{Path: "messages", Mode: dsl.StateAccessRead}),
			},
		},
		Resolve: func(spec registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			return registry.NewEdgeCondition(spec.Spec, func(context.Context, *state.State) bool { return true }), nil
		},
	}
	if err := reg.RegisterCondition(condition); err != nil {
		t.Fatalf("register condition: %v", err)
	}
	return reg
}

func primitiveTestPort(name, schemaType string, mode dsl.StateAccessMode, merge dsl.StateMergeStrategy, required bool) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name: name, Required: required, Schema: dsl.JSONSchema{"type": schemaType}, Mode: mode, MergeStrategy: merge,
	}
}

func capabilityTestPort(name, capability string, fields ...dsl.RelativeStateFieldRef) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name: name, Required: true, Capability: capability,
		Contract: dsl.RelativeStateContract{Fields: fields},
	}
}

func mustRegisterStateModule(t *testing.T, reg *registry.Registry, module dsl.StateModuleDefinition) {
	t.Helper()
	if err := reg.RegisterStateModule(module); err != nil {
		t.Fatalf("register state module: %v", err)
	}
}

func mustRegisterNodeType(t *testing.T, reg *registry.Registry, nodeType string, ports []dsl.StatePortDefinition) {
	t.Helper()
	if err := reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: nodeType, StatePorts: ports},
		Build: func(_ *registry.BuildContext, spec registry.ResolvedNodeSpec) (core.Node, error) {
			return &bindingTestNode{NodeInfo: core.NodeInfo{NodeID: spec.Spec.ID}}, nil
		},
	}); err != nil {
		t.Fatalf("register node type %q: %v", nodeType, err)
	}
}

func assertContractField(t *testing.T, contract state.Contract, path string, mode state.AccessMode, merge state.MergeStrategy) {
	t.Helper()
	for _, field := range contract.Fields {
		if field.Path.String() != path {
			continue
		}
		if field.Mode != mode || field.Merge != merge {
			t.Fatalf("contract field %q = mode %q merge %q, want mode %q merge %q", path, field.Mode, field.Merge, mode, merge)
		}
		return
	}
	t.Fatalf("contract field %q not found in %#v", path, contract.Fields)
}

type bindingTestNode struct {
	core.NodeInfo
}

func (n *bindingTestNode) Execute(core.Context, *state.Access) error { return nil }
