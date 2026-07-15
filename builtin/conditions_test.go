package builtin

import (
	"context"
	"testing"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestPlanStatusConditionUsesResolvedBinding(t *testing.T) {
	t.Parallel()
	path := state.Scope("planner", "state")
	definition := NewDefaultRegistry().Conditions[node.ConditionTypePlanStatusEquals]
	condition, err := definition.Resolve(registry.ResolvedConditionSpec{
		Spec:  dsl.GraphConditionSpec{Type: node.ConditionTypePlanStatusEquals, Config: map[string]any{"status": node.PlanStatusExecuting}},
		State: map[string]registry.ResolvedStateBinding{"plan": {Path: path}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	current := state.NewState()
	_ = state.SetPath(current, path.MustChild("status").String(), node.PlanStatusExecuting)
	if !condition.Match(context.Background(), current) {
		t.Fatal("expected status match")
	}
}

func TestSupervisorRouteConditionUsesResolvedBinding(t *testing.T) {
	t.Parallel()
	path := state.Scope("team", "supervisor")
	definition := NewDefaultRegistry().Conditions[node.ConditionTypeSupervisorRouteEquals]
	condition, err := definition.Resolve(registry.ResolvedConditionSpec{
		Spec:  dsl.GraphConditionSpec{Type: node.ConditionTypeSupervisorRouteEquals, Config: map[string]any{"worker_id": "researcher"}},
		State: map[string]registry.ResolvedStateBinding{"supervisor": {Path: path}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	current := state.NewState()
	_ = state.SetPath(current, path.MustChild(node.SupervisorFieldRoute).String(), "researcher")
	if !condition.Match(context.Background(), current) {
		t.Fatal("expected route match")
	}
}

func TestConversationToolCallConditionUsesBoundRoot(t *testing.T) {
	t.Parallel()
	path := state.Scope("loop", "conversation")
	access := state.NewEditingAccess(state.NewState())
	view, _ := conversationcap.Bind(access, path)
	_ = view.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
		llms.ToolCall{ID: "call", Type: "function", FunctionCall: &llms.FunctionCall{Name: "tool"}},
	}}})
	if !ConversationHasToolCalls(path).Match(context.Background(), access.State()) {
		t.Fatal("expected tool call match")
	}
}

func TestExpressionConditionEvaluatesRelativeToBinding(t *testing.T) {
	t.Parallel()
	path := state.Shared("ticket")
	condition, err := ExpressionConditions(path, ExpressionConditionConfig{Expressions: []Expression{{Value1: "status", Op: OperationEqual, Value2: "open"}}})
	if err != nil {
		t.Fatalf("condition: %v", err)
	}
	current := state.FromShared(map[string]any{"ticket": map[string]any{"status": "open"}})
	if !condition.Match(context.Background(), current) {
		t.Fatal("expected expression match")
	}
}
