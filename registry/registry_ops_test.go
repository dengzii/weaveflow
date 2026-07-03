package registry

import (
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
)

func TestRegisterStateFieldValidatesNameAndDuplicates(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.RegisterStateField(dsl.StateFieldDefinition{}); err == nil {
		t.Fatal("expected empty state field name error")
	}
	if err := reg.RegisterStateField(dsl.StateFieldDefinition{Name: " shared.answer "}); err != nil {
		t.Fatalf("register field: %v", err)
	}
	if _, ok := reg.StateFields["shared.answer"]; !ok {
		t.Fatalf("expected trimmed state field key, got %#v", reg.StateFields)
	}
	if err := reg.RegisterStateField(dsl.StateFieldDefinition{Name: "shared.answer"}); err == nil {
		t.Fatal("expected duplicate state field error")
	}
}

func TestRegisterNodeTypeValidatesTypeBuilderAndDuplicates(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.RegisterNodeType(NodeTypeDefinition{}); err == nil {
		t.Fatal("expected empty node type error")
	}
	if err := reg.RegisterNodeType(NodeTypeDefinition{NodeTypeSchema: dsl.NodeTypeSchema{Type: "custom"}}); err == nil {
		t.Fatal("expected nil builder error")
	}

	def := NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: " custom "},
		Build: func(*BuildContext, dsl.GraphNodeSpec) (core.Node, error) {
			return nil, nil
		},
	}
	if err := reg.RegisterNodeType(def); err != nil {
		t.Fatalf("register node type: %v", err)
	}
	if _, ok := reg.NodeTypes["custom"]; !ok {
		t.Fatalf("expected trimmed node type key, got %#v", reg.NodeTypes)
	}
	if err := reg.RegisterNodeType(def); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate node type error, got %v", err)
	}
}

func TestRegisterConditionValidatesTypeResolverAndDuplicates(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.RegisterCondition(ConditionDefinition{}); err == nil {
		t.Fatal("expected empty condition type error")
	}
	if err := reg.RegisterCondition(ConditionDefinition{ConditionSchema: dsl.ConditionSchema{Type: "custom"}}); err == nil {
		t.Fatal("expected nil resolver error")
	}

	def := ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{Type: " custom "},
		Resolve: func(dsl.GraphConditionSpec) (EdgeCondition, error) {
			return EdgeCondition{}, nil
		},
	}
	if err := reg.RegisterCondition(def); err != nil {
		t.Fatalf("register condition: %v", err)
	}
	if _, ok := reg.Conditions["custom"]; !ok {
		t.Fatalf("expected trimmed condition key, got %#v", reg.Conditions)
	}
	if err := reg.RegisterCondition(def); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate condition error, got %v", err)
	}
}

func TestRegistryAccessorsReturnClones(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.RegisterStateField(dsl.StateFieldDefinition{Name: "input"}); err != nil {
		t.Fatalf("register field: %v", err)
	}
	fields := reg.StateFieldDefinitions()
	delete(fields, "input")
	if _, ok := reg.StateFields["input"]; !ok {
		t.Fatal("state field accessor exposed mutable map")
	}
}
