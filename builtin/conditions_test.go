package builtin

import (
	"context"
	"strings"
	"testing"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/dsl"
	plannode "github.com/dengzii/weaveflow/node/plan"
	supervisornode "github.com/dengzii/weaveflow/node/supervisor"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

func TestPlanStatusConditionUsesResolvedBinding(t *testing.T) {
	t.Parallel()
	path := state.Scope("planner", "state")
	definition, ok := NewDefaultRegistry().FindCondition(plannode.ConditionTypePlanStatusEquals)
	if !ok {
		t.Fatal("plan status condition is not registered")
	}
	condition, err := definition.Resolve(registry.ResolvedConditionSpec{
		Spec:  dsl.GraphConditionSpec{Type: plannode.ConditionTypePlanStatusEquals, Config: map[string]any{"status": plannode.PlanStatusExecuting}},
		State: map[string]registry.ResolvedStateBinding{"plan": {Path: path}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	current := state.NewState()
	_ = state.SetPath(current, path.MustChild("status").String(), plannode.PlanStatusExecuting)
	if !mustMatchCondition(t, condition, current) {
		t.Fatal("expected status match")
	}
}

func TestSupervisorRouteConditionUsesResolvedBinding(t *testing.T) {
	t.Parallel()
	path := state.Scope("team", "supervisor")
	definition, ok := NewDefaultRegistry().FindCondition(supervisornode.ConditionTypeSupervisorRouteEquals)
	if !ok {
		t.Fatal("supervisor route condition is not registered")
	}
	condition, err := definition.Resolve(registry.ResolvedConditionSpec{
		Spec:  dsl.GraphConditionSpec{Type: supervisornode.ConditionTypeSupervisorRouteEquals, Config: map[string]any{"worker_id": "researcher"}},
		State: map[string]registry.ResolvedStateBinding{"supervisor": {Path: path}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	current := state.NewState()
	_ = state.SetPath(current, path.MustChild(supervisornode.SupervisorFieldRoute).String(), "researcher")
	if !mustMatchCondition(t, condition, current) {
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
	if !mustMatchCondition(t, ConversationHasToolCalls(path), access.State()) {
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
	if !mustMatchCondition(t, condition, current) {
		t.Fatal("expected expression match")
	}
}

func TestStateExpressionCombinesBoundInputs(t *testing.T) {
	t.Parallel()
	condition, err := StateExpression(map[string]state.Path{
		"price": state.Shared("cart", "price"), "quantity": state.Shared("cart", "quantity"), "vip": state.Shared("user", "vip"),
	}, "inputs.vip && inputs.price * inputs.quantity >= 100")
	if err != nil {
		t.Fatalf("StateExpression(): %v", err)
	}
	matching := state.FromShared(map[string]any{
		"cart": map[string]any{"price": 25, "quantity": 4}, "user": map[string]any{"vip": true},
	})
	if !mustMatchCondition(t, condition, matching) {
		t.Fatal("expected state expression match")
	}
	nonMatching := state.FromShared(map[string]any{
		"cart": map[string]any{"price": 10, "quantity": 4}, "user": map[string]any{"vip": true},
	})
	if mustMatchCondition(t, condition, nonMatching) {
		t.Fatal("unexpected state expression match")
	}
}

func TestStateExpressionFailsClosed(t *testing.T) {
	t.Parallel()
	t.Run("missing input", func(t *testing.T) {
		condition, err := StateExpression(map[string]state.Path{"value": state.Shared("value")}, "inputs.value > 0")
		if err != nil {
			t.Fatalf("StateExpression(): %v", err)
		}
		if mustMatchCondition(t, condition, state.NewState()) {
			t.Fatal("missing input must not match")
		}
	})
	t.Run("dynamic non boolean", func(t *testing.T) {
		condition, err := StateExpression(map[string]state.Path{"value": state.Shared("value")}, "inputs.value")
		if err != nil {
			t.Fatalf("StateExpression(): %v", err)
		}
		if _, err := condition.Match(context.Background(), state.FromShared(map[string]any{"value": "yes"})); err == nil {
			t.Fatal("non-boolean result must return an error")
		}
	})
}

func TestStateExpressionRejectsInvalidOrStaticNonBooleanExpression(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{"inputs.", "1 + 1"} {
		_, err := StateExpression(map[string]state.Path{"value": state.Shared("value")}, expression)
		if err == nil || (!strings.Contains(err.Error(), "compile CEL expression") && !strings.Contains(err.Error(), "not boolean")) {
			t.Fatalf("StateExpression(%q) error = %v", expression, err)
		}
	}
}

func TestStateExpressionConditionDefinitionUsesDynamicPorts(t *testing.T) {
	t.Parallel()
	definition, ok := NewDefaultRegistry().FindCondition(ConditionTypeStateExpression)
	if !ok {
		t.Fatal("state expression condition is not registered")
	}
	if definition.DynamicStatePorts == nil || definition.DynamicStatePorts.MinPorts != 1 || definition.DynamicStatePorts.Required {
		t.Fatalf("dynamic state ports = %#v", definition.DynamicStatePorts)
	}
	condition, err := definition.Resolve(registry.ResolvedConditionSpec{
		Spec:  dsl.GraphConditionSpec{Type: ConditionTypeStateExpression, Config: map[string]any{"expression": "inputs.ready"}},
		State: map[string]registry.ResolvedStateBinding{"ready": {Path: state.Shared("ready")}},
	})
	if err != nil {
		t.Fatalf("Resolve(): %v", err)
	}
	if !mustMatchCondition(t, condition, state.FromShared(map[string]any{"ready": true})) {
		t.Fatal("expected resolved condition match")
	}
}

func mustMatchCondition(t *testing.T, condition registry.EdgeCondition, current *state.State) bool {
	t.Helper()
	matched, err := condition.Match(context.Background(), current)
	if err != nil {
		t.Fatalf("condition match: %v", err)
	}
	return matched
}
