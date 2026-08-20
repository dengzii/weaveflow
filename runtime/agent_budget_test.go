package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

func TestAgentBudgetAccumulatesAndRejectsAfterResume(t *testing.T) {
	currentState := state.NewState()
	ctx := WithAgentStateProvider(context.Background(), func() *state.State { return currentState })
	ctx = WithAgentBudgetGuard(ctx, &sync.Mutex{})
	limits := AgentBudgetLimits{MaxIterations: 2, MaxToolCalls: 1, MaxTokens: 10, MaxCost: 0.5}
	if _, err := ConsumeAgentBudget(ctx, limits, AgentBudgetUsage{Iterations: 1, ToolCalls: 1, TotalTokens: 6, Cost: 0.25}); err != nil {
		t.Fatalf("first ConsumeAgentBudget() = %v", err)
	}
	budget, found, err := LoadAgentBudget(currentState)
	if err != nil || !found {
		t.Fatalf("LoadAgentBudget() = %#v, %v, found=%v", budget, err, found)
	}
	if budget.ToolCalls != 1 || budget.TotalTokens != 6 {
		t.Fatalf("stored budget = %#v", budget)
	}
	_, err = ConsumeAgentBudget(ctx, limits, AgentBudgetUsage{ToolCalls: 1})
	var executionErr core.ExecutionError
	if err == nil || !errors.As(err, &executionErr) {
		t.Fatalf("second tool call error = %v, want resource exhaustion", err)
	}
	resumed, _, err := LoadAgentBudget(currentState)
	if err != nil {
		t.Fatalf("LoadAgentBudget() after rejection = %v", err)
	}
	if resumed.ToolCalls != 1 || resumed.TotalTokens != 6 {
		t.Fatalf("budget after rejection = %#v, want unchanged", resumed)
	}
	if _, err := ConsumeAgentBudget(ctx, limits, AgentBudgetUsage{TotalTokens: 5}); err == nil {
		t.Fatal("token budget overflow error = nil")
	}
	if _, err := ConsumeAgentBudget(ctx, limits, AgentBudgetUsage{Cost: 0.3}); err == nil {
		t.Fatal("cost budget overflow error = nil")
	}
}
