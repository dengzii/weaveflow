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
	langgraph "github.com/smallnest/langgraphgo/graph"
)

const PendingApprovalStateKey = "pending_approval"

type ApprovalGateNode struct {
	NodeInfo
	StateScope       string
	InterruptMessage string
}

func NewApprovalGateNode() *ApprovalGateNode {
	id := uuid.New()
	return &ApprovalGateNode{
		NodeInfo: NodeInfo{
			NodeID:          "ApprovalGate_" + id.String(),
			NodeName:        "ApprovalGate",
			NodeDescription: "Pause execution for human approval of high-risk tool calls.",
		},
		InterruptMessage: "interrupt due to waiting for approval of high-risk action",
	}
}

func (n *ApprovalGateNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	pending, ok, err := n.consumePendingApproval(state)
	if err != nil {
		return state, err
	}
	if ok {
		return n.applyApprovalDecision(ctx, state, pending)
	}

	check := fruntime.ToolPolicyCheck(state)
	if check == nil || !n.needsApproval(check) {
		approval := fruntime.EnsureApproval(state)
		approval["status"] = "approved"
		approval["decided_at"] = time.Now().Format(time.RFC3339)
		return state, nil
	}

	details := n.buildInterruptDetails(check)
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "approval_gate.pending", details)
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":   "approval_gate",
		"status": "pending",
	})

	return state, &langgraph.NodeInterrupt{Node: n.NodeID, Value: n.effectiveInterruptMessage(details)}
}

func (n *ApprovalGateNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "approval_gate",
		Description: n.Description(),
		Config: map[string]any{
			"state_scope":       n.StateScope,
			"interrupt_message": n.InterruptMessage,
		},
	}
}

func (n *ApprovalGateNode) consumePendingApproval(state fruntime.State) (map[string]any, bool, error) {
	target := n.pendingInputState(state)
	if target == nil {
		return nil, false, nil
	}
	raw, exists := target[PendingApprovalStateKey]
	if !exists {
		return nil, false, nil
	}
	delete(target, PendingApprovalStateKey)
	if raw == nil {
		return nil, false, nil
	}

	switch typed := raw.(type) {
	case map[string]any:
		return typed, true, nil
	case fruntime.State:
		return map[string]any(typed), true, nil
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil, false, nil
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			return map[string]any{"status": text}, true, nil
		}
		return parsed, true, nil
	default:
		return nil, false, fmt.Errorf("approval_gate: pending_approval must be map or string, got %T", raw)
	}
}

func (n *ApprovalGateNode) pendingInputState(state fruntime.State) fruntime.State {
	if state == nil {
		return nil
	}
	if n.StateScope == "" {
		return state
	}
	return state.Scope(n.StateScope)
}

func (n *ApprovalGateNode) needsApproval(check fruntime.State) bool {
	action, _ := check["action"].(string)
	return strings.EqualFold(action, PolicyActionNeedsApproval)
}

func (n *ApprovalGateNode) applyApprovalDecision(ctx context.Context, state fruntime.State, decision map[string]any) (fruntime.State, error) {
	status, _ := decision["status"].(string)
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "approved"
	}

	approval := fruntime.EnsureApproval(state)
	approval["status"] = status
	approval["decided_at"] = time.Now().Format(time.RFC3339)

	if decisions, ok := decision["decisions"]; ok {
		approval["decisions"] = decisions
	}

	if status == "approved" {
		n.promoteApprovedCalls(state)
	}

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "approval_gate.decision", map[string]any{
		"status":   status,
		"decision": decision,
	})
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":   "approval_gate",
		"status": status,
	})

	return state, nil
}

func (n *ApprovalGateNode) promoteApprovedCalls(state fruntime.State) {
	check := fruntime.ToolPolicyCheck(state)
	if check == nil {
		return
	}
	decisions, ok := check["decisions"].([]map[string]any)
	if !ok {
		return
	}
	approved := make([]map[string]any, 0)
	for _, d := range decisions {
		action, _ := d["action"].(string)
		if strings.EqualFold(action, PolicyActionNeedsApproval) || strings.EqualFold(action, PolicyActionAllow) {
			promoted := make(map[string]any, len(d))
			for k, v := range d {
				promoted[k] = v
			}
			promoted["action"] = PolicyActionAllow
			approved = append(approved, promoted)
		}
	}
	check["approved_calls"] = approved
	check["action"] = PolicyActionAllow
}

func (n *ApprovalGateNode) buildInterruptDetails(check fruntime.State) map[string]any {
	decisions, _ := check["decisions"].([]map[string]any)
	pending := make([]map[string]any, 0)
	for _, d := range decisions {
		action, _ := d["action"].(string)
		if strings.EqualFold(action, PolicyActionNeedsApproval) {
			pending = append(pending, d)
		}
	}
	return map[string]any{
		"pending_decisions": pending,
		"total_pending":     len(pending),
	}
}

func (n *ApprovalGateNode) effectiveInterruptMessage(details map[string]any) string {
	message := strings.TrimSpace(n.InterruptMessage)
	if message == "" {
		message = "interrupt due to waiting for approval of high-risk action"
	}
	data, err := json.Marshal(details)
	if err != nil {
		return message
	}
	return message + "\n" + string(data)
}
