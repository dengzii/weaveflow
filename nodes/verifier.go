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
	defaultVerifierScope = "default"

	VerifierModeStep  = "step"
	VerifierModeFinal = "final"
	VerifierModeAuto  = "auto"

	VerificationPass         = "pass"
	VerificationFail         = "fail"
	VerificationPartial      = "partial"
	VerificationInconclusive = "inconclusive"

	VerificationActionContinue = "continue"
	VerificationActionRetry    = "retry"
	VerificationActionReplan   = "replan"
	VerificationActionFinalize = "finalize"
	VerificationActionClarify  = "clarify"
)

type VerifierNode struct {
	NodeInfo
	model            llms.Model
	StateScope       string
	Mode             string
	PlannerStatePath string
}

func NewVerifierNode(model llms.Model) *VerifierNode {
	id := uuid.New()
	return &VerifierNode{
		NodeInfo: NodeInfo{
			NodeID:          "Verifier_" + id.String(),
			NodeName:        "Verifier",
			NodeDescription: "Verify step or final results against acceptance criteria.",
		},
		model: model,
		Mode:  VerifierModeAuto,
	}
}

func (n *VerifierNode) effectiveScope() string {
	if n == nil || strings.TrimSpace(n.StateScope) == "" {
		return defaultVerifierScope
	}
	return strings.TrimSpace(n.StateScope)
}

func (n *VerifierNode) effectiveMode() string {
	if n == nil || strings.TrimSpace(n.Mode) == "" {
		return VerifierModeAuto
	}
	return strings.TrimSpace(n.Mode)
}

func (n *VerifierNode) effectivePlannerPath() string {
	if n == nil || strings.TrimSpace(n.PlannerStatePath) == "" {
		return fruntime.StateKeyPlanner
	}
	return strings.TrimSpace(n.PlannerStatePath)
}

func (n *VerifierNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	mode := n.resolveMode(state)

	var result *verificationResult
	var err error

	switch mode {
	case VerifierModeFinal:
		result, err = n.verifyFinal(ctx, state)
	default:
		result, err = n.verifyStep(ctx, state)
	}

	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "verifier.error", map[string]any{
			"mode":  mode,
			"error": err.Error(),
		})
		result = &verificationResult{
			Status:     VerificationInconclusive,
			Summary:    fmt.Sprintf("Verification error: %v", err),
			NextAction: VerificationActionContinue,
		}
	}

	n.applyResult(state, result, mode)

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "verifier.result", map[string]any{
		"mode":        mode,
		"status":      result.Status,
		"next_action": result.NextAction,
		"issues":      result.Issues,
		"summary":     result.Summary,
	})

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":        "verification_completed",
		"mode":        mode,
		"status":      result.Status,
		"next_action": result.NextAction,
	})

	return state, nil
}

func (n *VerifierNode) resolveMode(state fruntime.State) string {
	mode := n.effectiveMode()
	if mode != VerifierModeAuto {
		return mode
	}

	exec := state.Get(fruntime.StateKeyExecution)
	if exec != nil {
		route, _ := exec["route"].(string)
		if route == ExecutionRouteFinalize {
			return VerifierModeFinal
		}
	}
	return VerifierModeStep
}

func (n *VerifierNode) verifyStep(ctx context.Context, state fruntime.State) (*verificationResult, error) {
	plannerState := state.Get(fruntime.StateKeyPlanner)
	if plannerState == nil {
		return &verificationResult{
			Status:     VerificationPass,
			Summary:    "No planner state; defaulting to pass.",
			NextAction: VerificationActionContinue,
		}, nil
	}

	stepID, _ := plannerState["current_step_id"].(string)
	step := findStepByID(plannerState, stepID)
	if step == nil {
		return &verificationResult{
			Status:     VerificationPass,
			Summary:    "No current step found; defaulting to pass.",
			NextAction: VerificationActionContinue,
		}, nil
	}

	criteria := extractStringSlice(step, "acceptance_criteria")
	if len(criteria) == 0 {
		return &verificationResult{
			Status:     VerificationPass,
			Summary:    "No acceptance criteria defined; defaulting to pass.",
			NextAction: VerificationActionContinue,
		}, nil
	}

	observations := filterObservationsByStep(fruntime.Observations(state), stepID)
	stepResults := fruntime.StepResults(state)
	var stepResult map[string]any
	if stepResults != nil {
		if r, ok := stepResults[stepID].(map[string]any); ok {
			stepResult = r
		}
	}

	return n.callLLMVerification(ctx, "step", criteria, observations, stepResult, step)
}

func (n *VerifierNode) verifyFinal(ctx context.Context, state fruntime.State) (*verificationResult, error) {
	plannerState := state.Get(fruntime.StateKeyPlanner)
	objective := ""
	if plannerState != nil {
		objective, _ = plannerState["objective"].(string)
	}

	if objective == "" {
		req := state.Get(fruntime.StateKeyRequest)
		if req != nil {
			objective, _ = req["input"].(string)
		}
	}

	if objective == "" {
		return &verificationResult{
			Status:     VerificationPass,
			Summary:    "No objective to verify; defaulting to pass.",
			NextAction: VerificationActionFinalize,
		}, nil
	}

	observations := fruntime.Observations(state)
	evidence := fruntime.Evidence(state)

	conversation := fruntime.Conversation(state, n.effectiveScope())
	finalAnswer := conversation.FinalAnswer()

	return n.callLLMFinalVerification(ctx, objective, observations, evidence, finalAnswer)
}

func (n *VerifierNode) callLLMVerification(ctx context.Context, mode string, criteria []string, observations []map[string]any, stepResult map[string]any, step map[string]any) (*verificationResult, error) {
	if n.model == nil {
		return ruleBasedVerification(criteria, observations), nil
	}

	stepTitle, _ := step["title"].(string)
	prompt := buildStepVerificationPrompt(stepTitle, criteria, observations, stepResult)

	resp, err := n.model.GenerateContent(ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, verifierSystemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, prompt),
		},
		llms.WithTemperature(0.1),
	)
	if err != nil {
		return nil, fmt.Errorf("verifier LLM call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("verifier LLM returned no choices")
	}

	return parseVerificationResponse(resp.Choices[0].Content)
}

func (n *VerifierNode) callLLMFinalVerification(ctx context.Context, objective string, observations []map[string]any, evidence []map[string]any, finalAnswer string) (*verificationResult, error) {
	if n.model == nil {
		return &verificationResult{
			Status:     VerificationPass,
			Summary:    "No model available; defaulting to pass.",
			NextAction: VerificationActionFinalize,
		}, nil
	}

	prompt := buildFinalVerificationPrompt(objective, observations, evidence, finalAnswer)

	resp, err := n.model.GenerateContent(ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, verifierSystemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, prompt),
		},
		llms.WithTemperature(0.1),
	)
	if err != nil {
		return nil, fmt.Errorf("verifier final LLM call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("verifier final LLM returned no choices")
	}

	return parseVerificationResponse(resp.Choices[0].Content)
}

func (n *VerifierNode) applyResult(state fruntime.State, result *verificationResult, mode string) {
	v := state.Ensure(fruntime.StateKeyVerification)
	v["status"] = result.Status
	v["issues"] = result.Issues
	v["summary"] = result.Summary
	v["next_action"] = result.NextAction
	v["needs_replan"] = result.NextAction == VerificationActionReplan
	v["needs_retry"] = result.NextAction == VerificationActionRetry

	if mode == VerifierModeStep && result.Status == VerificationPass {
		n.markCurrentStepCompleted(state)
	}
	if mode == VerifierModeStep && result.NextAction == VerificationActionRetry {
		n.markCurrentStepReady(state)
	}
}

func (n *VerifierNode) markCurrentStepCompleted(state fruntime.State) {
	plannerState := state.Get(fruntime.StateKeyPlanner)
	if plannerState == nil {
		return
	}
	stepID, _ := plannerState["current_step_id"].(string)
	if stepID == "" {
		return
	}
	step := findStepByID(plannerState, stepID)
	if step != nil {
		step["status"] = "completed"
	}
}

func (n *VerifierNode) markCurrentStepReady(state fruntime.State) {
	plannerState := state.Get(fruntime.StateKeyPlanner)
	if plannerState == nil {
		return
	}
	stepID, _ := plannerState["current_step_id"].(string)
	if stepID == "" {
		return
	}
	step := findStepByID(plannerState, stepID)
	if step != nil {
		step["status"] = "ready"
	}
}

func (n *VerifierNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{}
	if scope := n.effectiveScope(); scope != defaultVerifierScope {
		config["state_scope"] = scope
	}
	if mode := n.effectiveMode(); mode != VerifierModeAuto {
		config["mode"] = mode
	}
	if plannerPath := n.effectivePlannerPath(); plannerPath != fruntime.StateKeyPlanner {
		config["planner_state_path"] = plannerPath
	}
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "verifier",
		Description: n.Description(),
		Config:      config,
	}
}

// --- internal types ---

type verificationResult struct {
	Status     string   `json:"status"`
	Issues     []string `json:"issues"`
	Summary    string   `json:"summary"`
	NextAction string   `json:"suggestion"`
}

// --- prompt building ---

const verifierSystemPrompt = `You are a verification agent. Your job is to check whether execution results satisfy the given criteria.

Always respond with a JSON object in this exact format:
{
  "status": "pass" | "fail" | "partial",
  "issues": ["list of issues found, empty if pass"],
  "summary": "one-sentence summary of verification result",
  "suggestion": "continue" | "retry" | "replan" | "finalize"
}

Rules:
- "pass" means all criteria are met.
- "fail" means critical criteria are not met.
- "partial" means some criteria are met but not all.
- "continue" means proceed to next step (use when status is pass).
- "retry" means the same step should be retried (minor/transient failure).
- "replan" means the plan needs fundamental changes.
- "finalize" means the overall task is done and we should produce the final answer.

Respond ONLY with valid JSON. No markdown, no explanation.`

func buildStepVerificationPrompt(stepTitle string, criteria []string, observations []map[string]any, stepResult map[string]any) string {
	var b strings.Builder

	b.WriteString("## Step\n")
	b.WriteString(stepTitle)
	b.WriteString("\n\n## Acceptance Criteria\n")
	for i, c := range criteria {
		fmt.Fprintf(&b, "%d. %s\n", i+1, c)
	}

	b.WriteString("\n## Observations\n")
	if len(observations) == 0 {
		b.WriteString("No observations recorded.\n")
	} else {
		for _, obs := range observations {
			source, _ := obs["source"].(string)
			summary, _ := obs["summary"].(string)
			errVal := obs["error"]
			fmt.Fprintf(&b, "- [%s] %s", source, summary)
			if errVal != nil {
				fmt.Fprintf(&b, " (ERROR: %v)", errVal)
			}
			b.WriteString("\n")
		}
	}

	if stepResult != nil {
		b.WriteString("\n## Step Result\n")
		if raw, err := json.Marshal(stepResult); err == nil {
			b.Write(raw)
		}
		b.WriteString("\n")
	}

	return b.String()
}

func buildFinalVerificationPrompt(objective string, observations []map[string]any, evidence []map[string]any, finalAnswer string) string {
	var b strings.Builder

	b.WriteString("## Objective\n")
	b.WriteString(objective)

	b.WriteString("\n\n## All Observations\n")
	if len(observations) == 0 {
		b.WriteString("No observations recorded.\n")
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

	if finalAnswer != "" {
		b.WriteString("\n## Current Answer Draft\n")
		b.WriteString(finalAnswer)
	}

	b.WriteString("\n\nDoes the collected evidence and answer satisfy the objective? Respond with verification JSON.")
	return b.String()
}

// --- parsing ---

func parseVerificationResponse(content string) (*verificationResult, error) {
	content = strings.TrimSpace(content)
	content = stripMarkdownCodeBlock(content)

	var result verificationResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return &verificationResult{
			Status:     VerificationInconclusive,
			Summary:    "Failed to parse LLM verification response.",
			Issues:     []string{fmt.Sprintf("parse error: %v; raw: %s", err, truncateSummary(content, 200))},
			NextAction: VerificationActionContinue,
		}, nil
	}

	result.Status = normalizeVerificationStatus(result.Status)
	result.NextAction = normalizeVerificationAction(result.NextAction)

	return &result, nil
}

func stripMarkdownCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}

func normalizeVerificationStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "passed", "success":
		return VerificationPass
	case "fail", "failed", "failure":
		return VerificationFail
	case "partial", "partially":
		return VerificationPartial
	default:
		return VerificationInconclusive
	}
}

func normalizeVerificationAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "continue":
		return VerificationActionContinue
	case "retry":
		return VerificationActionRetry
	case "replan":
		return VerificationActionReplan
	case "finalize":
		return VerificationActionFinalize
	case "clarify":
		return VerificationActionClarify
	default:
		return VerificationActionContinue
	}
}

// --- state helpers ---

func findStepByID(plannerState fruntime.State, stepID string) map[string]any {
	if stepID == "" {
		return nil
	}
	steps := extractPlanSteps(plannerState)
	for _, step := range steps {
		id, _ := step["id"].(string)
		if id == stepID {
			return step
		}
	}
	return nil
}

func extractStringSlice(m map[string]any, key string) []string {
	raw, ok := m[key]
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

func filterObservationsByStep(observations []map[string]any, stepID string) []map[string]any {
	if stepID == "" {
		return observations
	}
	var filtered []map[string]any
	for _, obs := range observations {
		if id, _ := obs["step_id"].(string); id == stepID {
			filtered = append(filtered, obs)
		}
	}
	return filtered
}

func ruleBasedVerification(criteria []string, observations []map[string]any) *verificationResult {
	hasError := false
	for _, obs := range observations {
		if obs["error"] != nil {
			hasError = true
			break
		}
	}

	if hasError {
		return &verificationResult{
			Status:     VerificationFail,
			Issues:     []string{"tool execution error detected in observations"},
			Summary:    "Step failed due to tool error.",
			NextAction: VerificationActionRetry,
		}
	}

	if len(observations) == 0 {
		return &verificationResult{
			Status:     VerificationInconclusive,
			Summary:    "No observations to verify against criteria.",
			NextAction: VerificationActionContinue,
		}
	}

	return &verificationResult{
		Status:     VerificationPass,
		Summary:    "Rule-based check passed (no model available for semantic verification).",
		NextAction: VerificationActionContinue,
	}
}
