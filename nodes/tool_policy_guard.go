package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	PolicyActionAllow         = "allow"
	PolicyActionDeny          = "deny"
	PolicyActionNeedsApproval = "needs_approval"
)

type ToolPolicyGuardNode struct {
	NodeInfo
	StateScope string
}

func NewToolPolicyGuardNode() *ToolPolicyGuardNode {
	id := uuid.New()
	return &ToolPolicyGuardNode{
		NodeInfo: NodeInfo{
			NodeID:          "ToolPolicyGuard_" + id.String(),
			NodeName:        "ToolPolicyGuard",
			NodeDescription: "Check tool calls against safety policies before execution.",
		},
	}
}

func (n *ToolPolicyGuardNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	toolCalls := n.extractToolCalls(state)
	if len(toolCalls) == 0 {
		check := state.Ensure(fruntime.StateKeyToolPolicyCheck)
		check["action"] = PolicyActionAllow
		check["decisions"] = []map[string]any{}
		check["blocked_calls"] = []map[string]any{}
		check["approved_calls"] = []map[string]any{}
		return state, nil
	}

	policy := state.Get(fruntime.StateKeyToolPolicy)
	decisions := make([]map[string]any, 0, len(toolCalls))
	blocked := make([]map[string]any, 0)
	approved := make([]map[string]any, 0)

	for _, tc := range toolCalls {
		name := toolCallName(tc)
		arguments := toolCallArguments(tc)
		decision := n.evaluateToolCall(name, arguments, policy)
		decision["tool_call_id"] = tc.ID
		decision["tool_name"] = name
		decisions = append(decisions, decision)

		switch decision["action"] {
		case PolicyActionDeny:
			blocked = append(blocked, decision)
		case PolicyActionNeedsApproval:
			// needs_approval goes to neither blocked nor approved until gate decides
		default:
			approved = append(approved, decision)
		}
	}

	aggregateAction := n.aggregateAction(decisions)

	check := state.Ensure(fruntime.StateKeyToolPolicyCheck)
	check["action"] = aggregateAction
	check["decisions"] = decisions
	check["blocked_calls"] = blocked
	check["approved_calls"] = approved
	check["checked_at"] = time.Now().Format(time.RFC3339)

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "tool_policy_guard.result", map[string]any{
		"action":         aggregateAction,
		"total_calls":    len(toolCalls),
		"blocked_count":  len(blocked),
		"approved_count": len(approved),
		"decisions":      decisions,
	})
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":           "tool_policy_guard",
		"action":         aggregateAction,
		"total_calls":    len(toolCalls),
		"blocked_count":  len(blocked),
		"approved_count": len(approved),
	})

	return state, nil
}

func (n *ToolPolicyGuardNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "tool_policy_guard",
		Description: n.Description(),
		Config: map[string]any{
			"state_scope": n.StateScope,
		},
	}
}

func (n *ToolPolicyGuardNode) extractToolCalls(state fruntime.State) []llms.ToolCall {
	conversation := state.Conversation(n.StateScope)
	messages := conversation.Messages()
	if len(messages) == 0 {
		return nil
	}
	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != llms.ChatMessageTypeAI {
		return nil
	}
	var calls []llms.ToolCall
	for _, part := range lastMessage.Parts {
		if tc, ok := part.(llms.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

func (n *ToolPolicyGuardNode) evaluateToolCall(name, arguments string, policy fruntime.State) map[string]any {
	if policy == nil {
		return map[string]any{"action": PolicyActionAllow, "reason": "no policy configured"}
	}

	allowlist := extractPolicyStringSlice(policy, "allowlist")
	if len(allowlist) > 0 && !containsIgnoreCase(allowlist, name) {
		return map[string]any{
			"action": PolicyActionDeny,
			"reason": fmt.Sprintf("tool %q not in allowlist", name),
		}
	}

	denylist := extractPolicyStringSlice(policy, "denylist")
	if containsIgnoreCase(denylist, name) {
		return map[string]any{
			"action": PolicyActionDeny,
			"reason": fmt.Sprintf("tool %q is in denylist", name),
		}
	}

	approvalPatterns := extractPolicyStringSlice(policy, "requires_approval_patterns")
	if matchesAnyPattern(name, arguments, approvalPatterns) {
		return map[string]any{
			"action": PolicyActionNeedsApproval,
			"reason": fmt.Sprintf("tool %q matches approval-required pattern", name),
		}
	}

	pathRestrictions := extractPolicyStringSlice(policy, "path_restrictions")
	if len(pathRestrictions) > 0 {
		if violation := checkPathRestrictions(arguments, pathRestrictions); violation != "" {
			return map[string]any{
				"action": PolicyActionDeny,
				"reason": fmt.Sprintf("path restriction violation: %s", violation),
			}
		}
	}

	domainRestrictions := extractPolicyStringSlice(policy, "domain_restrictions")
	if len(domainRestrictions) > 0 {
		if violation := checkDomainRestrictions(arguments, domainRestrictions); violation != "" {
			return map[string]any{
				"action": PolicyActionDeny,
				"reason": fmt.Sprintf("domain restriction violation: %s", violation),
			}
		}
	}

	return map[string]any{"action": PolicyActionAllow, "reason": "passed all policy checks"}
}

func (n *ToolPolicyGuardNode) aggregateAction(decisions []map[string]any) string {
	hasDeny := false
	hasNeedsApproval := false
	for _, d := range decisions {
		action, _ := d["action"].(string)
		switch action {
		case PolicyActionDeny:
			hasDeny = true
		case PolicyActionNeedsApproval:
			hasNeedsApproval = true
		}
	}
	if hasDeny {
		return PolicyActionDeny
	}
	if hasNeedsApproval {
		return PolicyActionNeedsApproval
	}
	return PolicyActionAllow
}

func extractPolicyStringSlice(policy fruntime.State, key string) []string {
	if policy == nil {
		return nil
	}
	raw, ok := policy[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

func containsIgnoreCase(list []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, item := range list {
		if strings.ToLower(strings.TrimSpace(item)) == target {
			return true
		}
	}
	return false
}

func matchesAnyPattern(toolName, arguments string, patterns []string) bool {
	lower := strings.ToLower(toolName)
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(lower, pattern) {
			return true
		}
		if strings.Contains(strings.ToLower(arguments), pattern) {
			return true
		}
	}
	return false
}

func checkPathRestrictions(arguments string, restrictions []string) string {
	if arguments == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return ""
	}
	for _, value := range extractStringValues(parsed) {
		for _, restricted := range restrictions {
			restricted = strings.TrimSpace(restricted)
			if restricted == "" {
				continue
			}
			if strings.Contains(value, restricted) {
				return fmt.Sprintf("path %q matches restricted pattern %q", value, restricted)
			}
		}
	}
	return ""
}

func checkDomainRestrictions(arguments string, restrictions []string) string {
	if arguments == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return ""
	}
	for _, value := range extractStringValues(parsed) {
		lower := strings.ToLower(value)
		for _, restricted := range restrictions {
			restricted = strings.ToLower(strings.TrimSpace(restricted))
			if restricted == "" {
				continue
			}
			if strings.Contains(lower, restricted) {
				return fmt.Sprintf("value %q matches restricted domain %q", value, restricted)
			}
		}
	}
	return ""
}

func extractStringValues(m map[string]any) []string {
	var values []string
	for _, v := range m {
		switch typed := v.(type) {
		case string:
			values = append(values, typed)
		case map[string]any:
			values = append(values, extractStringValues(typed)...)
		case []any:
			for _, item := range typed {
				if s, ok := item.(string); ok {
					values = append(values, s)
				}
				if nested, ok := item.(map[string]any); ok {
					values = append(values, extractStringValues(nested)...)
				}
			}
		}
	}
	return values
}
