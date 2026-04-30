package nodes

import (
	"context"
	"strings"
	"time"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
)

const (
	BudgetStatusOK       = "ok"
	BudgetStatusWarning  = "warning"
	BudgetStatusExceeded = "exceeded"

	defaultWarningThreshold = 0.8
)

type CostBudgetGuardNode struct {
	NodeInfo
	StateScope       string
	MaxTokens        int
	MaxToolCalls     int
	MaxIterations    int
	WarningThreshold float64
}

func NewCostBudgetGuardNode() *CostBudgetGuardNode {
	id := uuid.New()
	return &CostBudgetGuardNode{
		NodeInfo: NodeInfo{
			NodeID:          "CostBudgetGuard_" + id.String(),
			NodeName:        "CostBudgetGuard",
			NodeDescription: "Track token usage, tool calls, and iterations against configurable budget limits.",
		},
		WarningThreshold: defaultWarningThreshold,
	}
}

func (n *CostBudgetGuardNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	usage := n.collectUsage(state)
	limits := n.collectLimits(state)
	status, exceeded := n.evaluateBudget(usage, limits)

	budget := state.Ensure(fruntime.StateKeyBudget)
	budget["usage"] = usage
	budget["limits"] = limits
	budget["status"] = status
	budget["exceeded_limits"] = exceeded
	budget["checked_at"] = time.Now().Format(time.RFC3339)

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "cost_budget_guard.result", map[string]any{
		"status":          status,
		"usage":           usage,
		"limits":          limits,
		"exceeded_limits": exceeded,
	})
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":            "cost_budget_guard",
		"status":          status,
		"exceeded_limits": exceeded,
	})

	return state, nil
}

func (n *CostBudgetGuardNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope": n.StateScope,
	}
	if n.MaxTokens > 0 {
		config["max_tokens"] = n.MaxTokens
	}
	if n.MaxToolCalls > 0 {
		config["max_tool_calls"] = n.MaxToolCalls
	}
	if n.MaxIterations > 0 {
		config["max_iterations"] = n.MaxIterations
	}
	if n.WarningThreshold > 0 && n.WarningThreshold != defaultWarningThreshold {
		config["warning_threshold"] = n.WarningThreshold
	}
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "cost_budget_guard",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *CostBudgetGuardNode) collectUsage(state fruntime.State) map[string]any {
	totalTokens := 0
	llmCalls := 0
	toolCalls := 0
	iterations := 0

	if tokenUsage := readNestedMap(state, TokenUsageStateKey); tokenUsage != nil {
		if totals := readNestedMap(tokenUsage, "totals"); totals != nil {
			totalTokens = readIntMetric(totals, "total_tokens")
			llmCalls = readIntMetric(totals, "calls")
		}
	}

	observations := state.Observations()
	for _, obs := range observations {
		source, _ := obs["source"].(string)
		if strings.HasPrefix(source, "tool:") {
			toolCalls++
		}
	}

	conversation := state.Conversation(n.StateScope)
	iterations = conversation.IterationCount()

	return map[string]any{
		"total_tokens": totalTokens,
		"llm_calls":    llmCalls,
		"tool_calls":   toolCalls,
		"iterations":   iterations,
	}
}

func (n *CostBudgetGuardNode) collectLimits(state fruntime.State) map[string]any {
	limits := map[string]any{}

	maxTokens := n.MaxTokens
	maxToolCalls := n.MaxToolCalls
	maxIterations := n.MaxIterations

	budget := state.Get(fruntime.StateKeyBudget)
	if budget != nil {
		if stateLimits := readNestedMap(budget, "limits"); stateLimits != nil {
			if v := readIntMetric(stateLimits, "max_tokens"); v > 0 && maxTokens <= 0 {
				maxTokens = v
			}
			if v := readIntMetric(stateLimits, "max_tool_calls"); v > 0 && maxToolCalls <= 0 {
				maxToolCalls = v
			}
			if v := readIntMetric(stateLimits, "max_iterations"); v > 0 && maxIterations <= 0 {
				maxIterations = v
			}
		}
	}

	if maxTokens > 0 {
		limits["max_tokens"] = maxTokens
	}
	if maxToolCalls > 0 {
		limits["max_tool_calls"] = maxToolCalls
	}
	if maxIterations > 0 {
		limits["max_iterations"] = maxIterations
	}
	return limits
}

func (n *CostBudgetGuardNode) evaluateBudget(usage, limits map[string]any) (string, []string) {
	threshold := n.WarningThreshold
	if threshold <= 0 || threshold >= 1 {
		threshold = defaultWarningThreshold
	}

	overall := BudgetStatusOK
	var exceeded []string

	checks := []struct {
		usageKey string
		limitKey string
		label    string
	}{
		{"total_tokens", "max_tokens", "tokens"},
		{"tool_calls", "max_tool_calls", "tool_calls"},
		{"iterations", "max_iterations", "iterations"},
	}

	for _, check := range checks {
		limit := readIntMetric(limits, check.limitKey)
		if limit <= 0 {
			continue
		}
		current := readIntMetric(usage, check.usageKey)
		if current >= limit {
			exceeded = append(exceeded, check.label)
			overall = BudgetStatusExceeded
		} else if float64(current) >= float64(limit)*threshold && overall != BudgetStatusExceeded {
			overall = BudgetStatusWarning
		}
	}

	return overall, exceeded
}

func readNestedMap(state map[string]any, key string) map[string]any {
	if state == nil {
		return nil
	}
	raw, ok := state[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case map[string]any:
		return typed
	case fruntime.State:
		return map[string]any(typed)
	default:
		return nil
	}
}

func readIntMetric(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	raw, ok := m[key]
	if !ok {
		return 0
	}
	if v, ok := intValue(raw); ok {
		return v
	}
	return 0
}
