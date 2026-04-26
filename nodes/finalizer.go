package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	defaultFinalizerScope = "default"

	FinalStatusSuccess            = "success"
	FinalStatusFailed             = "failed"
	FinalStatusBlocked            = "blocked"
	FinalStatusNeedsClarification = "needs_clarification"
)

type FinalizerNode struct {
	NodeInfo
	model            llms.Model
	StateScope       string
	PlannerStatePath string
}

func NewFinalizerNode(model llms.Model) *FinalizerNode {
	id := uuid.New()
	return &FinalizerNode{
		NodeInfo: NodeInfo{
			NodeID:          "Finalizer_" + id.String(),
			NodeName:        "Finalizer",
			NodeDescription: "Generate the final answer from verification results, observations, and evidence.",
		},
		model: model,
	}
}

func (n *FinalizerNode) effectiveScope() string {
	if n == nil || strings.TrimSpace(n.StateScope) == "" {
		return defaultFinalizerScope
	}
	return strings.TrimSpace(n.StateScope)
}

func (n *FinalizerNode) effectivePlannerPath() string {
	if n == nil || strings.TrimSpace(n.PlannerStatePath) == "" {
		return fruntime.StateKeyPlanner
	}
	return strings.TrimSpace(n.PlannerStatePath)
}

func (n *FinalizerNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	conversation := fruntime.Conversation(state, n.effectiveScope())

	if n.isDirectMode(state) {
		answer := conversation.FinalAnswer()
		if answer == "" {
			answer = n.fallbackAnswer(state, nil)
		}
		outcome := FinalStatusSuccess

		final := fruntime.EnsureFinal(state)
		final["answer"] = answer
		final["status"] = outcome

		conversation.SetFinalAnswer(answer)

		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "finalizer.result", map[string]any{
			"status":        outcome,
			"answer_length": len(answer),
			"mode":          "direct",
		})
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
			"kind":   "finalized",
			"status": outcome,
			"mode":   "direct",
		})
		return state, nil
	}

	verification := fruntime.Verification(state)
	observations := fruntime.Observations(state)
	evidence := fruntime.Evidence(state)
	plannerState := fruntime.Planner(state)

	outcome := n.determineOutcome(verification)

	var answer string
	var err error

	switch outcome {
	case FinalStatusSuccess:
		answer, err = n.generateSuccessAnswer(ctx, state, plannerState, observations, evidence)
	case FinalStatusFailed:
		answer = n.generateFailureAnswer(verification, plannerState, observations)
	case FinalStatusNeedsClarification:
		answer = n.generateClarificationAnswer(verification, plannerState)
	case FinalStatusBlocked:
		answer = n.generateBlockedAnswer(verification, plannerState)
	}

	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "finalizer.error", map[string]any{
			"outcome": outcome,
			"error":   err.Error(),
		})
		answer = n.fallbackAnswer(state, observations)
		outcome = FinalStatusSuccess
	}

	final := fruntime.EnsureFinal(state)
	final["answer"] = answer
	final["status"] = outcome
	final["evidence"] = collectEvidenceRefs(evidence)

	conversation.SetFinalAnswer(answer)

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "finalizer.result", map[string]any{
		"status":         outcome,
		"answer_length":  len(answer),
		"evidence_count": len(evidence),
	})

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":   "finalized",
		"status": outcome,
	})

	return state, nil
}

func (n *FinalizerNode) isDirectMode(state fruntime.State) bool {
	orchestration := fruntime.Orchestration(state)
	if orchestration == nil {
		return false
	}
	mode, _ := orchestration["mode"].(string)
	return mode == "direct"
}

func (n *FinalizerNode) determineOutcome(verification fruntime.State) string {
	if verification == nil {
		return FinalStatusSuccess
	}

	nextAction, _ := verification["next_action"].(string)
	status, _ := verification["status"].(string)

	switch nextAction {
	case VerificationActionClarify:
		return FinalStatusNeedsClarification
	}

	switch status {
	case VerificationPass:
		return FinalStatusSuccess
	case VerificationFail:
		return FinalStatusFailed
	}

	exec := verification
	if route, _ := exec["route"].(string); route == ExecutionRouteBlocked {
		return FinalStatusBlocked
	}

	return FinalStatusSuccess
}

func (n *FinalizerNode) generateSuccessAnswer(ctx context.Context, state fruntime.State, plannerState fruntime.State, observations []map[string]any, evidence []map[string]any) (string, error) {
	if n.model == nil {
		return n.fallbackAnswer(state, observations), nil
	}

	objective := ""
	planSummary := ""
	if plannerState != nil {
		objective, _ = plannerState["objective"].(string)
		planSummary, _ = plannerState["summary"].(string)
	}
	if objective == "" {
		req := fruntime.Request(state)
		if req != nil {
			objective, _ = req["input"].(string)
		}
	}

	prompt := buildFinalizerPrompt(objective, planSummary, observations, evidence)

	resp, err := n.model.GenerateContent(ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, finalizerSystemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, prompt),
		},
		fruntime.WithLLMStreamingResponseEvent(),
		llms.WithTemperature(0.3),
	)
	if err != nil {
		return "", fmt.Errorf("finalizer LLM call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("finalizer LLM returned no choices")
	}

	return strings.TrimSpace(resp.Choices[0].Content), nil
}

func (n *FinalizerNode) generateFailureAnswer(verification fruntime.State, plannerState fruntime.State, observations []map[string]any) string {
	var b strings.Builder

	b.WriteString("I was unable to fully complete the task.\n\n")

	if verification != nil {
		if issues, ok := verification["issues"]; ok {
			b.WriteString("**Issues encountered:**\n")
			switch typed := issues.(type) {
			case []string:
				for _, issue := range typed {
					fmt.Fprintf(&b, "- %s\n", issue)
				}
			case []any:
				for _, issue := range typed {
					fmt.Fprintf(&b, "- %v\n", issue)
				}
			}
			b.WriteString("\n")
		}
	}

	if plannerState != nil {
		steps := extractPlanSteps(plannerState)
		var incomplete []string
		for _, step := range steps {
			status, _ := step["status"].(string)
			if status != "completed" {
				title, _ := step["title"].(string)
				incomplete = append(incomplete, title)
			}
		}
		if len(incomplete) > 0 {
			b.WriteString("**Incomplete steps:**\n")
			for _, title := range incomplete {
				fmt.Fprintf(&b, "- %s\n", title)
			}
			b.WriteString("\n")
		}
	}

	if partial := collectPartialResults(observations); partial != "" {
		b.WriteString("**Partial results:**\n")
		b.WriteString(partial)
		b.WriteString("\n")
	}

	return b.String()
}

func (n *FinalizerNode) generateClarificationAnswer(verification fruntime.State, plannerState fruntime.State) string {
	var b strings.Builder
	b.WriteString("I need more information to continue.\n\n")

	if verification != nil {
		if summary, _ := verification["summary"].(string); summary != "" {
			b.WriteString(summary)
			b.WriteString("\n\n")
		}
		if issues, ok := verification["issues"]; ok {
			b.WriteString("**Questions:**\n")
			switch typed := issues.(type) {
			case []string:
				for _, issue := range typed {
					fmt.Fprintf(&b, "- %s\n", issue)
				}
			case []any:
				for _, issue := range typed {
					fmt.Fprintf(&b, "- %v\n", issue)
				}
			}
		}
	}

	return b.String()
}

func (n *FinalizerNode) generateBlockedAnswer(verification fruntime.State, plannerState fruntime.State) string {
	var b strings.Builder
	b.WriteString("The task is currently blocked and cannot proceed.\n\n")

	if verification != nil {
		if summary, _ := verification["summary"].(string); summary != "" {
			b.WriteString("**Reason:** ")
			b.WriteString(summary)
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (n *FinalizerNode) fallbackAnswer(state fruntime.State, observations []map[string]any) string {
	conversation := fruntime.Conversation(state, n.effectiveScope())
	if fa := conversation.FinalAnswer(); fa != "" {
		return fa
	}

	messages := conversation.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llms.ChatMessageTypeAI {
			if text := extractText(messages[i]); text != "" {
				return text
			}
		}
	}

	if partial := collectPartialResults(observations); partial != "" {
		return partial
	}

	return "Task completed but no answer was generated."
}

func (n *FinalizerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{}
	if scope := n.effectiveScope(); scope != defaultFinalizerScope {
		config["state_scope"] = scope
	}
	if plannerPath := n.effectivePlannerPath(); plannerPath != fruntime.StateKeyPlanner {
		config["planner_state_path"] = plannerPath
	}
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "finalizer",
		Description: n.Description(),
		Config:      config,
	}
}

// --- prompts ---

const finalizerSystemPrompt = `You are a final answer generator. Based on the task objective, plan summary, observations, and evidence collected during execution, produce a clear, complete, and well-structured final answer.

Rules:
- Synthesize information from all observations and evidence.
- Reference specific evidence when making claims.
- Be concise but thorough.
- If the task was a question, answer it directly.
- If the task was an action, confirm what was done and the outcome.
- Do NOT include metadata about the plan or execution process unless directly relevant.`

func buildFinalizerPrompt(objective string, planSummary string, observations []map[string]any, evidence []map[string]any) string {
	var b strings.Builder

	b.WriteString("## Objective\n")
	b.WriteString(objective)
	b.WriteString("\n")

	if planSummary != "" {
		b.WriteString("\n## Plan Summary\n")
		b.WriteString(planSummary)
		b.WriteString("\n")
	}

	b.WriteString("\n## Observations\n")
	if len(observations) == 0 {
		b.WriteString("No observations.\n")
	} else {
		for _, obs := range observations {
			source, _ := obs["source"].(string)
			summary, _ := obs["summary"].(string)
			fmt.Fprintf(&b, "- [%s] %s\n", source, summary)
		}
	}

	if len(evidence) > 0 {
		b.WriteString("\n## Evidence\n")
		for _, ev := range evidence {
			evType, _ := ev["type"].(string)
			content, _ := ev["content"].(string)
			fmt.Fprintf(&b, "- [%s] %s\n", evType, content)
		}
	}

	b.WriteString("\nGenerate the final answer based on the above information.")
	return b.String()
}

// --- helpers ---

func collectEvidenceRefs(evidence []map[string]any) []string {
	if len(evidence) == 0 {
		return nil
	}
	var refs []string
	for _, ev := range evidence {
		if ref, ok := ev["artifact_ref"].(string); ok && ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func collectPartialResults(observations []map[string]any) string {
	var parts []string
	for _, obs := range observations {
		if obs["error"] != nil {
			continue
		}
		summary, _ := obs["summary"].(string)
		if summary != "" {
			parts = append(parts, summary)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// suppress unused import warning for json in non-LLM paths
var _ = json.Marshal
