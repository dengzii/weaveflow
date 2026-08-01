package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestResolvedConditionReceivesProjectedBoundState(t *testing.T) {
	t.Parallel()
	reg := conditionContractRegistry(t)
	workflow, err := NewBuilder(reg).Build(conditionContractDefinition(), nil)
	if err != nil {
		t.Fatalf("BuildGraph(): %v", err)
	}

	next, err := workflow.resolveNextNodes(context.Background(), "source", state.FromShared(map[string]any{
		"allowed": "yes",
		"secret":  "hidden",
	}))
	if err != nil {
		t.Fatalf("resolve next nodes: %v", err)
	}
	if len(next) != 1 || next[0] != "matched" {
		t.Fatalf("next nodes = %#v, want [matched]", next)
	}
}

func TestResolvedConditionValidatesRequiredBoundState(t *testing.T) {
	t.Parallel()
	workflow, err := NewBuilder(conditionContractRegistry(t)).Build(conditionContractDefinition(), nil)
	if err != nil {
		t.Fatalf("BuildGraph(): %v", err)
	}

	_, err = workflow.resolveNextNodes(context.Background(), "source", state.FromShared(map[string]any{
		"secret": "hidden",
	}))
	if err == nil || !strings.Contains(err.Error(), `required read path "shared.allowed" is missing`) {
		t.Fatalf("resolve next nodes error = %v", err)
	}
}

func conditionContractRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	if err := reg.RegisterStateModule(dsl.StateModuleDefinition{
		Name: "test.condition", Version: "1",
		Fields: []dsl.StateFieldDefinition{
			{Path: "shared.allowed", Schema: dsl.JSONSchema{"type": "string"}},
			{Path: "shared.secret", Schema: dsl.JSONSchema{"type": "string"}},
		},
	}); err != nil {
		t.Fatalf("register state module: %v", err)
	}
	if err := reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "noop",
			StatePorts: []dsl.StatePortDefinition{{
				Name: "input", Required: true, Schema: dsl.JSONSchema{"type": "string"},
				Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			return node.NewFuncNode(node.Spec{ID: resolved.Spec.ID}, func(core.Context, *state.Access) error {
				return nil
			}), nil
		},
	}); err != nil {
		t.Fatalf("register node type: %v", err)
	}
	if err := reg.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type: "allowed",
			StatePorts: []dsl.StatePortDefinition{{
				Name: "value", Required: true, Schema: dsl.JSONSchema{"type": "string"},
				Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			return registry.NewEdgeCondition(resolved.Spec, func(_ context.Context, current *state.State) bool {
				allowed, _ := state.ReadPath(current, "shared.allowed")
				_, secretVisible := state.ReadPath(current, "shared.secret")
				return allowed == "yes" && !secretVisible
			}), nil
		},
	}); err != nil {
		t.Fatalf("register condition: %v", err)
	}
	return reg
}

func conditionContractDefinition() dsl.GraphDefinition {
	return dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: []dsl.StateModuleRef{{Name: "test.condition", Version: "1"}},
		EntryPoint:   "source",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "source", Type: "noop", State: map[string]dsl.StateBinding{"input": {Path: "shared.allowed"}}},
			{ID: "matched", Type: "noop", State: map[string]dsl.StateBinding{"input": {Path: "shared.allowed"}}},
			{ID: "fallback", Type: "noop", State: map[string]dsl.StateBinding{"input": {Path: "shared.allowed"}}},
		},
		Edges: []dsl.GraphEdgeSpec{
			{
				From: "source", To: "matched",
				Condition: &dsl.GraphConditionSpec{
					Type: "allowed", State: map[string]dsl.StateBinding{"value": {Path: "shared.allowed"}},
				},
			},
			{From: "source", To: "fallback"},
			{From: "matched", To: dsl.EndNodeRef},
			{From: "fallback", To: dsl.EndNodeRef},
		},
	}
}
