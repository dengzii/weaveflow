package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

func testStateModule() dsl.StateModuleDefinition {
	return dsl.StateModuleDefinition{
		Name: "test", Version: "1",
		Fields: []dsl.StateFieldDefinition{{Path: "shared.input", Schema: dsl.JSONSchema{"type": "string"}}},
		Capabilities: []dsl.StateCapabilityDefinition{{
			ID: "test.object.v1", Schema: dsl.JSONSchema{"type": "object"},
			Fields: []dsl.StateCapabilityFieldDefinition{{Name: "value", Schema: dsl.JSONSchema{"type": "string"}, MergeStrategy: dsl.StateMergeReplace}},
		}},
	}
}

func TestRegistryGraphSchemaExportsV2ModulesAndBindings(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if err := reg.RegisterStateModule(testStateModule()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	if err := reg.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "custom", StatePorts: []dsl.StatePortDefinition{{
			Name: "input", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
		}}},
		Build: func(*BuildContext, ResolvedNodeSpec) (core.Node, error) { return nil, nil },
	}); err != nil {
		t.Fatalf("register node: %v", err)
	}
	payload, err := json.Marshal(reg.JSONSchema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	text := string(payload)
	for _, required := range []string{`"state_modules"`, `"state"`, `"input"`, `"2.0"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("graph schema missing %s: %s", required, text)
		}
	}
	for _, legacy := range []string{`"state_schema"`, `"state_scope"`} {
		if strings.Contains(text, legacy) {
			t.Fatalf("graph schema contains legacy property %s: %s", legacy, text)
		}
	}
}

func TestRegisterReducerValidatesVersionAndExportsDiscovery(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	for _, identifier := range []string{"sum", "sum.v", "sum.v0", "sum.v1.extra"} {
		if err := reg.RegisterReducer(identifier, state.SumReducer{}); err == nil {
			t.Fatalf("expected invalid reducer ID %q", identifier)
		}
	}
	if err := reg.RegisterReducer("sum.v1", state.SumReducer{}); err != nil {
		t.Fatalf("register reducer: %v", err)
	}
	if err := reg.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "counter", StatePorts: []dsl.StatePortDefinition{{
			Name: "total", Schema: dsl.JSONSchema{"type": "number"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace,
		}}},
		Build: func(*BuildContext, ResolvedNodeSpec) (core.Node, error) { return nil, nil },
	}); err != nil {
		t.Fatalf("register node type: %v", err)
	}
	if _, ok := reg.FindReducer("sum.v1"); !ok {
		t.Fatal("registered reducer was not discoverable")
	}
	if got := reg.ReducerIDs(); len(got) != 1 || got[0] != "sum.v1" {
		t.Fatalf("reducer IDs = %#v", got)
	}
	payload, err := json.Marshal(reg.JSONSchema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if !strings.Contains(string(payload), `"reducer":{"enum":["sum.v1"]}`) {
		t.Fatalf("graph schema missing reducer discovery: %s", payload)
	}
}

func TestRegisterStateModuleValidatesAndIndexesMetadata(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if err := reg.RegisterStateModule(dsl.StateModuleDefinition{}); err == nil {
		t.Fatal("expected empty module error")
	}
	module := testStateModule()
	if err := reg.RegisterStateModule(module); err != nil {
		t.Fatalf("register module: %v", err)
	}
	if _, ok := reg.FindStateModule("test", "1"); !ok {
		t.Fatalf("module not indexed: %#v", reg.StateModuleDefinitions())
	}
	if _, ok := reg.FindCapability("test.object.v1"); !ok {
		t.Fatalf("capability not indexed: %#v", reg.CapabilityDefinitions())
	}
	if _, ok := reg.StateFieldDefinitions()["shared.input"]; !ok {
		t.Fatal("field path not indexed")
	}
	if err := reg.RegisterStateModule(module); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate module error, got %v", err)
	}
}

func TestRegisterStateModuleRejectsReservedAndDuplicateMetadata(t *testing.T) {
	t.Parallel()
	reserved := testStateModule()
	reserved.Fields[0].Path = "runtime.secret"
	if err := NewRegistry().RegisterStateModule(reserved); err == nil {
		t.Fatal("expected reserved path error")
	}

	duplicate := testStateModule()
	duplicate.Fields = append(duplicate.Fields, duplicate.Fields[0])
	if err := NewRegistry().RegisterStateModule(duplicate); err == nil {
		t.Fatal("expected duplicate field error")
	}

	root := testStateModule()
	root.Fields[0].Path = "shared"
	if err := NewRegistry().RegisterStateModule(root); err == nil || !strings.Contains(err.Error(), "below section") {
		t.Fatalf("expected section root error, got %v", err)
	}

	reg := NewRegistry()
	if err := reg.RegisterStateModule(testStateModule()); err != nil {
		t.Fatalf("register first module: %v", err)
	}
	duplicatePath := testStateModule()
	duplicatePath.Name = "other"
	duplicatePath.Capabilities = nil
	if err := reg.RegisterStateModule(duplicatePath); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected cross-module field path error, got %v", err)
	}
	duplicateCapability := testStateModule()
	duplicateCapability.Name = "capability-owner"
	duplicateCapability.Fields = nil
	if err := reg.RegisterStateModule(duplicateCapability); err == nil || !strings.Contains(err.Error(), "capability") || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected cross-module capability id error, got %v", err)
	}
}

func TestRegisterNodeTypeValidatesPortsAndBuilder(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if err := reg.RegisterNodeType(NodeTypeDefinition{}); err == nil {
		t.Fatal("expected empty node type error")
	}
	def := NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: " custom ", StatePorts: []dsl.StatePortDefinition{{
			Name: " input ", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
		}}},
		Build: func(*BuildContext, ResolvedNodeSpec) (core.Node, error) { return nil, nil },
	}
	if err := reg.RegisterNodeType(def); err != nil {
		t.Fatalf("register node type: %v", err)
	}
	stored, ok := reg.FindNodeType("custom")
	if !ok || len(stored.StatePorts) != 1 {
		t.Fatalf("state ports not normalized: %#v", stored)
	}
	if stored.StatePorts[0].Name != "input" || stored.NodeTypeSchema.StatePorts[0].Name != "input" {
		t.Fatalf("state port normalization was not retained: %#v", stored)
	}
}

func TestRegisterNodeTypeInGroupCreatesAndAppends(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	definition := func(nodeType string) NodeTypeDefinition {
		return NodeTypeDefinition{
			NodeTypeSchema: dsl.NodeTypeSchema{Type: nodeType},
			Build:          func(*BuildContext, ResolvedNodeSpec) (core.Node, error) { return nil, nil },
		}
	}

	if err := reg.RegisterNodeTypeInGroup(" Models ", definition("llm_turn")); err != nil {
		t.Fatalf("register first grouped node type: %v", err)
	}
	if err := reg.RegisterNodeTypeInGroup("Models", definition("text_generation")); err != nil {
		t.Fatalf("register second grouped node type: %v", err)
	}
	group, ok := reg.FindNodeGroup("Models")
	if !ok {
		t.Fatal("node group was not created")
	}
	if group.Name != "Models" || len(group.NodeTypes) != 2 || group.NodeTypes[0] != "llm_turn" || group.NodeTypes[1] != "text_generation" {
		t.Fatalf("node group = %#v", group)
	}

	groups := reg.NodeGroupDefinitions()
	cloned := groups["Models"]
	cloned.NodeTypes[0] = "changed"
	groups["Models"] = cloned
	delete(groups, "Models")
	storedGroup, _ := reg.FindNodeGroup("Models")
	if storedGroup.NodeTypes[0] != "llm_turn" {
		t.Fatalf("node group getter exposed mutable data: %#v", storedGroup)
	}
}

func TestRegisterNodeTypeInGroupRejectsEmptyGroup(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	err := reg.RegisterNodeTypeInGroup(" ", NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "custom"},
		Build:          func(*BuildContext, ResolvedNodeSpec) (core.Node, error) { return nil, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "group") {
		t.Fatalf("expected group error, got %v", err)
	}
	if len(reg.NodeTypeDefinitions()) != 0 || len(reg.NodeGroupDefinitions()) != 0 {
		t.Fatalf("invalid grouped registration changed registry: nodes=%#v groups=%#v", reg.NodeTypeDefinitions(), reg.NodeGroupDefinitions())
	}
}

func TestRegisterConditionValidatesPortsAndResolver(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	def := ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{Type: "custom", StatePorts: []dsl.StatePortDefinition{{
			Name: "value", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
		}}},
		Resolve: func(ResolvedConditionSpec) (EdgeCondition, error) { return EdgeCondition{}, nil },
	}
	if err := reg.RegisterCondition(def); err != nil {
		t.Fatalf("register condition: %v", err)
	}
	storedCondition, ok := reg.FindCondition("custom")
	if !ok || len(storedCondition.StatePorts) != 1 {
		t.Fatalf("state ports not normalized: %#v", storedCondition)
	}
}

func TestRegisterNodeTypeValidatesAndClonesDynamicStatePorts(t *testing.T) {
	t.Parallel()
	dynamic := &dsl.DynamicStatePortDefinition{
		NamePattern: " [A-Za-z_][A-Za-z0-9_]* ", MinPorts: 1, MaxPorts: 4,
		Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
	}
	reg := NewRegistry()
	if err := reg.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "dynamic", DynamicStatePorts: dynamic},
		Build:          func(*BuildContext, ResolvedNodeSpec) (core.Node, error) { return nil, nil },
	}); err != nil {
		t.Fatalf("register dynamic node type: %v", err)
	}
	definition, _ := reg.FindNodeType("dynamic")
	stored := definition.DynamicStatePorts
	if stored == nil || stored.NamePattern != "[A-Za-z_][A-Za-z0-9_]*" || stored.MinPorts != 1 || stored.MaxPorts != 4 {
		t.Fatalf("dynamic state ports = %#v", stored)
	}
	dynamic.Schema["type"] = "string"
	if stored.Schema["type"] != "object" {
		t.Fatal("dynamic state port schema was not cloned")
	}
}

func TestRegisterNodeTypeRejectsInvalidDynamicStatePorts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dynamic dsl.DynamicStatePortDefinition
		want    string
	}{
		{name: "pattern", dynamic: dsl.DynamicStatePortDefinition{NamePattern: "[", Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace}, want: "pattern"},
		{name: "schema", dynamic: dsl.DynamicStatePortDefinition{NamePattern: "[a-z]+", Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace}, want: "schema"},
		{name: "mode", dynamic: dsl.DynamicStatePortDefinition{NamePattern: "[a-z]+", Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace}, want: "read mode"},
		{name: "merge", dynamic: dsl.DynamicStatePortDefinition{NamePattern: "[a-z]+", Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeMerge}, want: "replace"},
		{name: "count", dynamic: dsl.DynamicStatePortDefinition{NamePattern: "[a-z]+", MinPorts: 2, MaxPorts: 1, Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace}, want: "max_ports"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewRegistry().RegisterNodeType(NodeTypeDefinition{
				NodeTypeSchema: dsl.NodeTypeSchema{Type: "dynamic", DynamicStatePorts: &test.dynamic},
				Build:          func(*BuildContext, ResolvedNodeSpec) (core.Node, error) { return nil, nil },
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestRegisterNodeTypeRejectsInvalidPortContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		port dsl.StatePortDefinition
		want string
	}{
		{
			name: "primitive mode",
			port: dsl.StatePortDefinition{Name: "value", Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessMode("invalid"), MergeStrategy: dsl.StateMergeReplace},
			want: "invalid mode",
		},
		{
			name: "primitive merge",
			port: dsl.StatePortDefinition{Name: "value", Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeStrategy("invalid")},
			want: "invalid merge strategy",
		},
		{
			name: "relative mode",
			port: dsl.StatePortDefinition{Name: "value", Capability: "test.object.v1", Contract: dsl.RelativeStateContract{Fields: []dsl.RelativeStateFieldRef{{Path: "value", Mode: dsl.StateAccessMode("invalid")}}}},
			want: "invalid mode",
		},
		{
			name: "duplicate relative field",
			port: dsl.StatePortDefinition{Name: "value", Capability: "test.object.v1", Contract: dsl.RelativeStateContract{Fields: []dsl.RelativeStateFieldRef{{Path: " value ", Mode: dsl.StateAccessRead}, {Path: "value", Mode: dsl.StateAccessWrite}}}},
			want: "duplicated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reg := NewRegistry()
			err := reg.RegisterNodeType(NodeTypeDefinition{
				NodeTypeSchema: dsl.NodeTypeSchema{Type: "custom", StatePorts: []dsl.StatePortDefinition{test.port}},
				Build:          func(*BuildContext, ResolvedNodeSpec) (core.Node, error) { return nil, nil },
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestRegistryMetadataGettersReturnClones(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if err := reg.RegisterStateModule(testStateModule()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	modules := reg.StateModuleDefinitions()
	module := modules[StateModuleKey("test", "1")]
	module.Fields[0].Schema["type"] = "number"
	module.Capabilities[0].Schema["type"] = "array"
	module.Capabilities[0].Fields[0].Schema["type"] = "boolean"
	modules[StateModuleKey("test", "1")] = module
	delete(modules, StateModuleKey("test", "1"))
	if len(reg.StateModuleDefinitions()) != 1 {
		t.Fatal("module getter exposed mutable map")
	}
	stored, _ := reg.FindStateModule("test", "1")
	if stored.Fields[0].Schema["type"] != "string" || stored.Capabilities[0].Schema["type"] != "object" || stored.Capabilities[0].Fields[0].Schema["type"] != "string" {
		t.Fatalf("module getter exposed nested schema metadata: %#v", stored)
	}
	stored.Fields[0].Schema["type"] = "number"
	stored.Capabilities[0].Fields[0].Schema["type"] = "boolean"
	fresh, _ := reg.FindStateModule("test", "1")
	if fresh.Fields[0].Schema["type"] != "string" || fresh.Capabilities[0].Fields[0].Schema["type"] != "string" {
		t.Fatalf("module lookup exposed nested schema metadata: %#v", fresh)
	}
}
