package runtime

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

type AgentBudget struct {
	Iterations  int     `json:"iterations"`
	ToolCalls   int     `json:"tool_calls"`
	TotalTokens int     `json:"total_tokens"`
	TotalCost   float64 `json:"total_cost"`
}

type AgentBudgetUsage struct {
	Iterations  int
	ToolCalls   int
	TotalTokens int
	Cost        float64
}

type AgentBudgetLimits struct {
	MaxIterations int
	MaxToolCalls  int
	MaxTokens     int
	MaxCost       float64
}

var agentBudgetPath = state.Internal("agent", "budget")

type agentBudgetGuardKey struct{}

func WithAgentBudgetGuard(ctx context.Context, guard *sync.Mutex) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, agentBudgetGuardKey{}, guard)
}

func LoadAgentBudget(currentState *state.State) (AgentBudget, bool, error) {
	if currentState == nil {
		return AgentBudget{}, false, nil
	}
	value, found := state.ReadPath(currentState, agentBudgetPath.String())
	if !found {
		return AgentBudget{}, false, nil
	}
	budget, err := decodeAgentBudget(value)
	if err != nil {
		return AgentBudget{}, true, err
	}
	return budget, true, nil
}

func StoreAgentBudget(currentState *state.State, budget AgentBudget) error {
	if currentState == nil {
		return nil
	}
	if budget.Iterations < 0 || budget.ToolCalls < 0 || budget.TotalTokens < 0 || budget.TotalCost < 0 {
		return fmt.Errorf("agent budget values cannot be negative")
	}
	return state.SetPath(currentState, agentBudgetPath.String(), budget)
}

func ConsumeAgentBudget(ctx context.Context, limits AgentBudgetLimits, usage AgentBudgetUsage) (AgentBudget, error) {
	if usage.Iterations < 0 || usage.ToolCalls < 0 || usage.TotalTokens < 0 || usage.Cost < 0 {
		return AgentBudget{}, fmt.Errorf("agent budget usage cannot be negative")
	}
	currentState, ok := AgentStateFromContext(ctx)
	if !ok || currentState == nil {
		return AgentBudget{}, nil
	}
	guard, _ := ctx.Value(agentBudgetGuardKey{}).(*sync.Mutex)
	if guard != nil {
		guard.Lock()
		defer guard.Unlock()
	}
	current, _, err := LoadAgentBudget(currentState)
	if err != nil {
		return AgentBudget{}, err
	}
	next := AgentBudget{
		Iterations:  current.Iterations + usage.Iterations,
		ToolCalls:   current.ToolCalls + usage.ToolCalls,
		TotalTokens: current.TotalTokens + usage.TotalTokens,
		TotalCost:   current.TotalCost + usage.Cost,
	}
	if err := validateAgentBudgetLimits(next, limits); err != nil {
		return current, err
	}
	if err := StoreAgentBudget(currentState, next); err != nil {
		return current, err
	}
	return next, nil
}

func validateAgentBudgetLimits(budget AgentBudget, limits AgentBudgetLimits) error {
	checks := []struct {
		kind   string
		limit  float64
		actual float64
	}{
		{"iterations", float64(limits.MaxIterations), float64(budget.Iterations)},
		{"tool_calls", float64(limits.MaxToolCalls), float64(budget.ToolCalls)},
		{"tokens", float64(limits.MaxTokens), float64(budget.TotalTokens)},
		{"cost", limits.MaxCost, budget.TotalCost},
	}
	for _, check := range checks {
		if check.limit > 0 && check.actual > check.limit {
			return core.NewExecutionError(core.ErrorResourceExhausted, "agent budget exceeded", nil, map[string]any{
				"kind": check.kind, "limit": check.limit, "actual": check.actual,
			})
		}
	}
	return nil
}

func decodeAgentBudget(value any) (AgentBudget, error) {
	budget := AgentBudget{}
	switch typed := value.(type) {
	case AgentBudget:
		budget = typed
	case map[string]any:
		budget.Iterations = intBudgetValue(typed["iterations"])
		budget.ToolCalls = intBudgetValue(typed["tool_calls"])
		budget.TotalTokens = intBudgetValue(typed["total_tokens"])
		budget.TotalCost, _ = typed["total_cost"].(float64)
	default:
		return AgentBudget{}, fmt.Errorf("agent budget has invalid type %T", value)
	}
	if budget.Iterations < 0 || budget.ToolCalls < 0 || budget.TotalTokens < 0 || budget.TotalCost < 0 {
		return AgentBudget{}, fmt.Errorf("agent budget values cannot be negative")
	}
	return budget, nil
}

func intBudgetValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}
