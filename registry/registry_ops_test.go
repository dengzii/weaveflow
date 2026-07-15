package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
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
	if _, ok := reg.StateModules[StateModuleKey("test", "1")]; !ok {
		t.Fatalf("module not indexed: %#v", reg.StateModules)
	}
	if _, ok := reg.Capabilities["test.object.v1"]; !ok {
		t.Fatalf("capability not indexed: %#v", reg.Capabilities)
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
	if len(reg.NodeTypes["custom"].StatePorts) != 1 {
		t.Fatalf("state ports not normalized: %#v", reg.NodeTypes["custom"])
	}
	if reg.NodeTypes["custom"].StatePorts[0].Name != "input" || reg.NodeTypes["custom"].NodeTypeSchema.StatePorts[0].Name != "input" {
		t.Fatalf("state port normalization was not retained: %#v", reg.NodeTypes["custom"])
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
	if len(reg.Conditions["custom"].StatePorts) != 1 {
		t.Fatalf("state ports not normalized: %#v", reg.Conditions["custom"])
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
	if len(reg.StateModules) != 1 {
		t.Fatal("module getter exposed mutable map")
	}
	stored := reg.StateModules[StateModuleKey("test", "1")]
	if stored.Fields[0].Schema["type"] != "string" || stored.Capabilities[0].Schema["type"] != "object" || stored.Capabilities[0].Fields[0].Schema["type"] != "string" {
		t.Fatalf("module getter exposed nested schema metadata: %#v", stored)
	}
}
