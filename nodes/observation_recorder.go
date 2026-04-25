package nodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	defaultObservationRecorderScope  = "default"
	observationMaxSummaryLen         = 500
	observationDefaultLLMConfidence  = 0.8
	observationToolSuccessConfidence = 1.0
	observationToolFailureConfidence = 0.0
)

type ObservationRecorderNode struct {
	NodeInfo
	StateScope       string
	PlannerStatePath string
}

func NewObservationRecorderNode() *ObservationRecorderNode {
	id := uuid.New()
	return &ObservationRecorderNode{
		NodeInfo: NodeInfo{
			NodeID:          "ObservationRecorder_" + id.String(),
			NodeName:        "ObservationRecorder",
			NodeDescription: "Record structured observations and evidence from tool and LLM outputs.",
		},
	}
}

func (n *ObservationRecorderNode) effectiveScope() string {
	if n == nil || strings.TrimSpace(n.StateScope) == "" {
		return defaultObservationRecorderScope
	}
	return strings.TrimSpace(n.StateScope)
}

func (n *ObservationRecorderNode) effectivePlannerPath() string {
	if n == nil || strings.TrimSpace(n.PlannerStatePath) == "" {
		return fruntime.StateKeyPlanner
	}
	return strings.TrimSpace(n.PlannerStatePath)
}

func (n *ObservationRecorderNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	conversation := fruntime.Conversation(state, n.effectiveScope())
	messages := conversation.Messages()
	if len(messages) == 0 {
		return state, nil
	}

	currentStepID := resolveCurrentStepID(state, n.effectivePlannerPath())
	now := time.Now().Format(time.RFC3339)

	var recorded []map[string]any

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		switch msg.Role {
		case llms.ChatMessageTypeTool:
			obs, evs := n.recordToolMessage(ctx, msg, currentStepID, now)
			for _, ob := range obs {
				fruntime.AppendObservation(state, ob)
				recorded = append(recorded, ob)
			}
			for _, ev := range evs {
				fruntime.AppendEvidence(state, ev)
			}

		case llms.ChatMessageTypeAI:
			if hasToolCalls(msg) {
				break
			}
			obs := n.recordLLMMessage(ctx, msg, currentStepID, now)
			if obs != nil {
				fruntime.AppendObservation(state, obs)
				recorded = append(recorded, obs)
			}
		}

		if msg.Role == llms.ChatMessageTypeTool {
			continue
		}
		break
	}

	if currentStepID != "" && len(recorded) > 0 {
		fruntime.SetStepResult(state, currentStepID, map[string]any{
			"observations_count": len(recorded),
			"recorded_at":        now,
		})
	}

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "observations.recorded", map[string]any{
		"step_id":      currentStepID,
		"count":        len(recorded),
		"observations": recorded,
	})

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":    "observations_recorded",
		"step_id": currentStepID,
		"count":   len(recorded),
	})

	return state, nil
}

func (n *ObservationRecorderNode) recordToolMessage(ctx context.Context, msg llms.MessageContent, stepID string, ts string) ([]map[string]any, []map[string]any) {
	var observations []map[string]any
	var evidences []map[string]any

	for _, part := range msg.Parts {
		resp, ok := part.(llms.ToolCallResponse)
		if !ok {
			continue
		}

		content := resp.Content
		isError := isToolErrorContent(content)
		confidence := observationToolSuccessConfidence
		if isError {
			confidence = observationToolFailureConfidence
		}

		summary := truncateSummary(content, observationMaxSummaryLen)
		source := fmt.Sprintf("tool:%s", resp.Name)

		obs := map[string]any{
			"source":     source,
			"summary":    summary,
			"error":      nil,
			"confidence": confidence,
			"step_id":    stepID,
			"timestamp":  ts,
		}
		if isError {
			obs["error"] = map[string]any{
				"tool":    resp.Name,
				"message": content,
			}
		}

		ref, _ := fruntime.SaveJSONArtifactBestEffort(ctx, "observation.tool_output", map[string]any{
			"tool":    resp.Name,
			"call_id": resp.ToolCallID,
			"content": content,
			"error":   isError,
		})
		if ref.ID != "" {
			obs["raw_ref"] = ref.ID
		}

		observations = append(observations, obs)

		evidences = append(evidences, map[string]any{
			"type":         "tool_output",
			"content":      summary,
			"source":       source,
			"artifact_ref": ref.ID,
		})
	}

	return observations, evidences
}

func (n *ObservationRecorderNode) recordLLMMessage(_ context.Context, msg llms.MessageContent, stepID string, ts string) map[string]any {
	text := extractText(msg)
	if text == "" {
		return nil
	}

	return map[string]any{
		"source":     "llm",
		"summary":    truncateSummary(text, observationMaxSummaryLen),
		"error":      nil,
		"confidence": observationDefaultLLMConfidence,
		"step_id":    stepID,
		"timestamp":  ts,
	}
}

func (n *ObservationRecorderNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{}
	if scope := n.effectiveScope(); scope != defaultObservationRecorderScope {
		config["state_scope"] = scope
	}
	if plannerPath := n.effectivePlannerPath(); plannerPath != fruntime.StateKeyPlanner {
		config["planner_state_path"] = plannerPath
	}
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "observation_recorder",
		Description: n.Description(),
		Config:      config,
	}
}

// --- helpers ---

func resolveCurrentStepID(state fruntime.State, plannerPath string) string {
	planner := fruntime.Planner(state)
	if planner == nil {
		return ""
	}
	id, _ := planner["current_step_id"].(string)
	return id
}

func hasToolCalls(msg llms.MessageContent) bool {
	for _, part := range msg.Parts {
		if _, ok := part.(llms.ToolCall); ok {
			return true
		}
	}
	return false
}

func isToolErrorContent(content string) bool {
	lower := strings.ToLower(content)
	return strings.HasPrefix(lower, "error:") ||
		strings.HasPrefix(lower, "error ") ||
		strings.Contains(lower, "\"error\":")
}

func truncateSummary(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
