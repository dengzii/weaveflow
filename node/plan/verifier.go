package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	executioncap "github.com/dengzii/weaveflow/capability/execution"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

const NodeTypePlanVerifier = "plan_verifier"

const defaultGroundedCriticPrompt = `You are a strict source-grounded critic.
Audit the step result only against the enumerated evidence supplied in the payload.
Reject material factual claims that are contradicted, unsupported, numerically imprecise, or based on topology or behavior not shown by evidence.
Every supported claim must cite one or more valid evidence refs such as E1. Do not use outside knowledge.
Historical failed checks may be superseded only by later evidence that directly proves the same issue was corrected.
Return strict JSON matching the response schema.`

type VerificationRequest struct {
	Objective string
	Step      plancap.Step
	Evidence  []plancap.Evidence
	Config    map[string]any
}

type VerificationResult struct {
	Status    string
	Summary   string
	Evidence  []plancap.Evidence
	Retryable bool
}

type VerificationHandler func(context.Context, VerificationRequest) (VerificationResult, error)

type verifierHandlerKey struct{}

func WithVerifier(ctx context.Context, handler VerificationHandler) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, verifierHandlerKey{}, handler)
}

func VerifierFromContext(ctx context.Context) VerificationHandler {
	if ctx == nil {
		return nil
	}
	handler, _ := ctx.Value(verifierHandlerKey{}).(VerificationHandler)
	return handler
}

type VerifierNode struct {
	core.NodeBase
	VerifierID          string
	MaxAttempts         int
	MinimumEvidence     int
	MaxEvidence         int
	AllowNoOp           bool
	RequireTestEvidence bool
	CriticEnabled       bool
	CriticModelID       string
	CriticPrompt        string
	PlanPath            state.Path
	ExecutionPath       state.Path
	ConversationPath    state.Path
	VerifierConfig      map[string]any
}

func NewVerifierNode(options ...core.NodeOption) *VerifierNode {
	target := &VerifierNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeTypePlanVerifier,
			Description: "Verify the current plan step using evidence and a deterministic verifier.",
		}),
		MaxAttempts: 2,
		MaxEvidence: 64,
		AllowNoOp:   true,
	}
	applyNodeOptions(&target.NodeBase, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *VerifierNode) Validate() error {
	if n == nil {
		return errors.New("plan verifier node is nil")
	}
	if err := n.NodeBase.Validate(); err != nil {
		return err
	}
	if n.PlanPath.Empty() || n.ExecutionPath.Empty() || n.ConversationPath.Empty() {
		return fmt.Errorf("plan verifier node %q requires plan, execution, and conversation paths", n.ID())
	}
	if n.MaxAttempts <= 0 {
		return fmt.Errorf("plan verifier node %q max_attempts must be greater than zero", n.ID())
	}
	if n.MinimumEvidence < 0 || n.MaxEvidence <= 0 || n.MinimumEvidence > n.MaxEvidence {
		return fmt.Errorf("plan verifier node %q has invalid evidence limits", n.ID())
	}
	return nil
}

func (n *VerifierNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.NodeBase, NodeTypePlanVerifier, map[string]any{
		"verifier_id":           n.VerifierID,
		"max_attempts":          n.MaxAttempts,
		"minimum_evidence":      n.MinimumEvidence,
		"max_evidence":          n.MaxEvidence,
		"allow_no_op":           n.AllowNoOp,
		"require_test_evidence": n.RequireTestEvidence,
		"critic_enabled":        n.CriticEnabled,
		"critic_model_id":       n.CriticModelID,
		"critic_prompt":         n.CriticPrompt,
		"config":                n.VerifierConfig,
	}, map[string]state.Path{"plan": n.PlanPath, "execution": n.ExecutionPath, "conversation": n.ConversationPath})
}

func VerifierNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypePlanVerifier,
			Title:       "Plan Verifier",
			Description: "Verify the current plan step using tool evidence and a deterministic verifier.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"verifier_id":           dsl.JSONSchema{"type": "string"},
					"max_attempts":          dsl.JSONSchema{"type": "integer", "minimum": 1},
					"minimum_evidence":      dsl.JSONSchema{"type": "integer", "minimum": 0},
					"max_evidence":          dsl.JSONSchema{"type": "integer", "minimum": 1},
					"allow_no_op":           dsl.JSONSchema{"type": "boolean", "description": "Allow an explicit no-op strategy for evidence-backed analysis."},
					"require_test_evidence": dsl.JSONSchema{"type": "boolean", "description": "Require a successful test or verification tool result."},
					"critic_enabled":        dsl.JSONSchema{"type": "boolean"},
					"critic_model_id":       dsl.JSONSchema{"type": "string"},
					"critic_prompt": dsl.JSONSchema{
						"type": "string", "title": "Grounded Critic Prompt", "x-control": "textarea",
					},
					"config": dsl.JSONSchema{"type": "object"},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("plan", "Current step and verification outcome.", plancap.CapabilityID, true,
				capabilityField(plancap.FieldObjective, dsl.StateAccessRead),
				capabilityField(plancap.FieldSteps, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldCurrentIndex, dsl.StateAccessRead),
			),
			capabilityPort("execution", "Current execution result.", executioncap.CapabilityID, true,
				capabilityField(executioncap.FieldCurrentStep, dsl.StateAccessRead),
				capabilityField(executioncap.FieldStepResults, dsl.StateAccessWrite),
			),
			capabilityPort("conversation", "Tool messages used as verification evidence.", conversationcap.CapabilityID, true,
				capabilityField(conversationcap.FieldMessages, dsl.StateAccessRead),
				capabilityField(conversationcap.FieldFinalAnswer, dsl.StateAccessRead),
			),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			planPath, err := resolvedPath(resolved, "plan")
			if err != nil {
				return nil, err
			}
			executionPath, err := resolvedPath(resolved, "execution")
			if err != nil {
				return nil, err
			}
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			target := NewVerifierNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			target.VerifierID = config.String(spec.Config, "verifier_id")
			if value, ok := config.Bool(spec.Config, "critic_enabled"); ok {
				target.CriticEnabled = value
			}
			target.CriticModelID = config.String(spec.Config, "critic_model_id")
			if _, exists := spec.Config["critic_prompt"]; exists {
				target.CriticPrompt = config.String(spec.Config, "critic_prompt")
			}
			if value, ok := config.Int(spec.Config, "max_attempts"); ok {
				target.MaxAttempts = value
			}
			if value, ok := config.Int(spec.Config, "minimum_evidence"); ok {
				target.MinimumEvidence = value
			}
			if value, ok := config.Int(spec.Config, "max_evidence"); ok {
				target.MaxEvidence = value
			}
			if value, ok := config.Bool(spec.Config, "allow_no_op"); ok {
				target.AllowNoOp = value
			}
			if value, ok := config.Bool(spec.Config, "require_test_evidence"); ok {
				target.RequireTestEvidence = value
			}
			if target.MinimumEvidence < 0 || target.MaxEvidence <= 0 || target.MinimumEvidence > target.MaxEvidence {
				return nil, fmt.Errorf("build plan verifier node %q: invalid evidence limits", spec.ID)
			}
			if config, ok := spec.Config["config"].(map[string]any); ok {
				target.VerifierConfig = cloneVerifierConfig(config)
			}
			target.PlanPath, target.ExecutionPath, target.ConversationPath = planPath, executionPath, conversationPath
			return target, nil
		},
	}
}

func (n *VerifierNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *VerifierNode) execute(ctx core.Context, access *state.Access) error {
	planner, err := plancap.Bind(access, n.PlanPath)
	if err != nil {
		return err
	}
	execution, err := executioncap.Bind(access, n.ExecutionPath)
	if err != nil {
		return err
	}
	conversation, err := conversationcap.Bind(access, n.ConversationPath)
	if err != nil {
		return err
	}
	planValue := planner.Value()
	steps := planner.Steps()
	index := intValue(planValue[planFieldCurrentIndex])
	if index < 0 || index >= len(steps) {
		return fmt.Errorf("plan verifier node: current step index %d is out of range", index)
	}
	step := steps[index]
	currentStep := execution.CurrentStep()
	if currentID := stringValue(currentStep["id"]); currentID != "" && currentID != step.ID {
		return fmt.Errorf("plan verifier node: execution step %q does not match plan step %q", currentID, step.ID)
	}
	if step.Result == "" {
		step.Result = stringValue(currentStep["result"])
	}
	if result := strings.TrimSpace(conversation.FinalAnswer()); result != "" {
		step.Result = result
	}
	messages := conversation.Messages()
	evidence := collectEvidence(messages)
	step.ModelCalls, step.ToolCalls = executionCounts(messages)
	if startedAt, parseErr := time.Parse(time.RFC3339Nano, step.StartedAt); parseErr == nil {
		step.DurationMillis = time.Since(startedAt).Milliseconds()
	}
	step.Evidence = mergeEvidenceLimit(step.Evidence, evidence, n.MaxEvidence)
	step.VerificationAttempts++
	request := VerificationRequest{
		Objective: stringValue(planValue[planFieldObjective]),
		Step:      step,
		Evidence:  append([]plancap.Evidence(nil), step.Evidence...),
		Config:    cloneVerifierConfig(n.VerifierConfig),
	}
	if request.Config == nil {
		request.Config = make(map[string]any, 4)
	}
	request.Config["minimum_evidence"] = n.MinimumEvidence
	request.Config["allow_no_op"] = n.AllowNoOp
	request.Config["require_test_evidence"] = n.RequireTestEvidence
	result := genericVerification(request)
	strategy := strings.TrimSpace(step.VerificationStrategy)
	if handler := VerifierFromContext(ctx); handler != nil && n.VerifierID != "" && strings.EqualFold(strategy, n.VerifierID) {
		result, err = handler(ctx, request)
		if err != nil {
			return fmt.Errorf("plan verifier node: %w", err)
		}
		if len(result.Evidence) > 0 {
			step.Evidence = mergeEvidenceLimit(step.Evidence, result.Evidence, n.MaxEvidence)
		}
	}
	if result.Status == VerificationStatusPassed && n.CriticEnabled {
		request.Evidence = append([]plancap.Evidence(nil), step.Evidence...)
		result, err = n.runGroundedCritic(ctx, request)
		step.ModelCalls++
		if err != nil {
			return fmt.Errorf("plan verifier node: grounded critic: %w", err)
		}
	}
	if result.Status == "" {
		result.Status = VerificationStatusFailed
	}
	if result.Summary == "" {
		result.Summary = "verification returned no summary"
	}
	step.VerificationStatus = result.Status
	step.VerificationSummary = limitEvidenceText(result.Summary)
	step.AttemptHistory = append(step.AttemptHistory, plancap.Attempt{
		Number: step.VerificationAttempts, Result: step.Result, VerificationStatus: result.Status,
		Summary: step.VerificationSummary, Evidence: append([]plancap.Evidence(nil), step.Evidence...),
	})
	if len(step.AttemptHistory) > 16 {
		step.AttemptHistory = step.AttemptHistory[len(step.AttemptHistory)-16:]
	}
	steps[index] = step
	if err := planner.SetSteps(steps); err != nil {
		return err
	}
	return execution.SetStepResult(step.ID, plancap.EncodeSteps([]plancap.Step{step})[0])
}

type groundedCriticClaim struct {
	Claim        string   `json:"claim"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type groundedCriticOutput struct {
	Passed            bool                  `json:"passed"`
	Summary           string                `json:"summary"`
	SupportedClaims   []groundedCriticClaim `json:"supported_claims"`
	UnsupportedClaims []string              `json:"unsupported_claims"`
}

func (n *VerifierNode) runGroundedCritic(ctx core.Context, request VerificationRequest) (VerificationResult, error) {
	if len(request.Evidence) == 0 {
		return VerificationResult{Status: VerificationStatusRetry, Summary: "grounded critic requires observable evidence", Retryable: true}, nil
	}
	model := ctx.Model(n.CriticModelID)
	if model == nil {
		return VerificationResult{}, fmt.Errorf("model %q is not available", effectiveModelID(n.CriticModelID))
	}
	prompt, validRefs, err := groundedCriticPayload(request)
	if err != nil {
		return VerificationResult{}, err
	}
	temperature := 0.0
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		ModelID: effectiveModelID(n.CriticModelID),
		Mode:    llms.ModelModeChat,
		Messages: []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveCriticPrompt()),
			llms.TextParts(llms.ChatMessageTypeHuman, "Audit this step result:\n\n"+prompt),
		},
		Thinking:       llms.ThinkingModeLow,
		Temperature:    &temperature,
		MaxTokens:      1600,
		ResponseName:   "grounded_plan_critique",
		ResponseSchema: groundedCriticSchema(),
		StrictResponse: true,
	})
	if err != nil {
		return VerificationResult{}, err
	}
	if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
		return VerificationResult{}, errors.New("model returned no choices")
	}
	var output groundedCriticOutput
	if err := json.Unmarshal([]byte(response.Choices[0].Content), &output); err != nil {
		return VerificationResult{}, fmt.Errorf("decode response: %w", err)
	}
	return groundedCriticDecision(output, validRefs), nil
}

func (n *VerifierNode) effectiveCriticPrompt() string {
	if n == nil || strings.TrimSpace(n.CriticPrompt) == "" {
		return defaultGroundedCriticPrompt
	}
	return n.CriticPrompt
}

func groundedCriticPayload(request VerificationRequest) (string, map[string]struct{}, error) {
	const evidenceBudget = 24 * 1024
	start := max(len(request.Evidence)-12, 0)
	selected := request.Evidence[start:]
	perEvidence := min(8192, evidenceBudget/max(len(selected), 1))
	evidence := make([]map[string]any, 0, len(selected))
	validRefs := make(map[string]struct{}, len(selected))
	for index, item := range selected {
		ref := fmt.Sprintf("E%d", start+index+1)
		validRefs[ref] = struct{}{}
		evidence = append(evidence, map[string]any{
			"ref": ref, "tool_id": item.ToolID, "status": item.Status,
			"summary": textLimit(item.Summary, perEvidence), "error": textLimit(item.Error, perEvidence),
		})
	}
	payload := map[string]any{
		"objective": request.Objective,
		"step": map[string]any{
			"id": request.Step.ID, "title": request.Step.Title, "description": request.Step.Description,
			"deliverables": request.Step.Deliverables, "acceptance_criteria": request.Step.AcceptanceCriteria,
			"result": textLimit(request.Step.Result, 12000),
		},
		"evidence": evidence,
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("encode payload: %w", err)
	}
	return string(encoded), validRefs, nil
}

func groundedCriticDecision(output groundedCriticOutput, validRefs map[string]struct{}) VerificationResult {
	unsupported := make([]string, 0, len(output.UnsupportedClaims)+1)
	for _, claim := range output.UnsupportedClaims {
		if claim = strings.TrimSpace(claim); claim != "" {
			unsupported = append(unsupported, claim)
		}
	}
	supported := make([]string, 0, len(output.SupportedClaims))
	for _, claim := range output.SupportedClaims {
		claim.Claim = strings.TrimSpace(claim.Claim)
		refs := make([]string, 0, len(claim.EvidenceRefs))
		for _, rawRef := range claim.EvidenceRefs {
			ref := strings.Trim(strings.TrimSpace(rawRef), "[]")
			if _, ok := validRefs[ref]; !ok {
				unsupported = append(unsupported, fmt.Sprintf("claim %q references unknown evidence %q", claim.Claim, rawRef))
				continue
			}
			refs = append(refs, ref)
		}
		if claim.Claim == "" || len(refs) == 0 {
			unsupported = append(unsupported, "a supported claim is empty or has no valid evidence refs")
			continue
		}
		supported = append(supported, fmt.Sprintf("%s [%s]", claim.Claim, strings.Join(refs, ", ")))
	}
	if len(supported) == 0 {
		unsupported = append(unsupported, "critic returned no evidence-grounded supported claims")
	}
	summary := strings.TrimSpace(output.Summary)
	if summary == "" {
		summary = "grounded critic returned no summary"
	}
	if !output.Passed || len(unsupported) > 0 {
		if len(unsupported) > 0 {
			summary += "; unsupported: " + strings.Join(unsupported, "; ")
		}
		return VerificationResult{Status: VerificationStatusRetry, Summary: limitEvidenceText(summary), Retryable: true}
	}
	return VerificationResult{
		Status:  VerificationStatusPassed,
		Summary: limitEvidenceText(summary + "; grounded claims: " + strings.Join(supported, "; ")),
	}
}

func groundedCriticSchema() state.JSONSchema {
	return state.JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"passed":  map[string]any{"type": "boolean"},
			"summary": map[string]any{"type": "string"},
			"supported_claims": map[string]any{
				"type": "array", "maxItems": 12,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"claim": map[string]any{"type": "string"},
						"evidence_refs": map[string]any{
							"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1,
						},
					},
					"required": []string{"claim", "evidence_refs"}, "additionalProperties": false,
				},
			},
			"unsupported_claims": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 12,
			},
		},
		"required":             []string{"passed", "summary", "supported_claims", "unsupported_claims"},
		"additionalProperties": false,
	}
}

func genericVerification(request VerificationRequest) VerificationResult {
	if strings.EqualFold(strings.TrimSpace(request.Step.VerificationStrategy), "no-op") {
		if allowed, ok := request.Config["allow_no_op"].(bool); ok && !allowed {
			return VerificationResult{Status: VerificationStatusRetry, Summary: "no-op verification is disabled by configuration", Retryable: true}
		}
		if strings.TrimSpace(request.Step.Result) == "" {
			return VerificationResult{Status: VerificationStatusRetry, Summary: "step returned no result", Retryable: true}
		}
		return VerificationResult{Status: VerificationStatusPassed, Summary: "explicit no-op verification accepted the non-empty analysis result."}
	}
	if minimum := intConfig(request.Config, "minimum_evidence"); minimum > 0 && len(request.Evidence) < minimum {
		return VerificationResult{Status: VerificationStatusRetry, Summary: fmt.Sprintf("verification requires at least %d evidence item(s)", minimum), Retryable: true}
	}
	if len(request.Evidence) == 0 {
		return VerificationResult{Status: VerificationStatusRetry, Summary: "no tool evidence was recorded", Retryable: true}
	}
	for _, item := range request.Evidence {
		if strings.EqualFold(item.Status, "failed") || item.Error != "" {
			return VerificationResult{Status: VerificationStatusRetry, Summary: "tool evidence contains a failure: " + limitEvidenceText(item.Error), Retryable: true}
		}
	}
	if strings.TrimSpace(request.Step.Result) == "" {
		return VerificationResult{Status: VerificationStatusRetry, Summary: "step returned no result", Retryable: true}
	}
	requiresTest := requiresTestEvidence(request.Step.AcceptanceCriteria)
	if configured, ok := request.Config["require_test_evidence"].(bool); ok {
		requiresTest = requiresTest || configured
	}
	if requiresTest && !hasTestEvidence(request.Evidence) {
		return VerificationResult{Status: VerificationStatusRetry, Summary: "acceptance criteria require a successful test or verification result", Retryable: true}
	}
	return VerificationResult{Status: VerificationStatusPassed, Summary: "deliverable is supported by successful tool evidence."}
}

func collectEvidence(messages []llms.MessageContent) []plancap.Evidence {
	result := make([]plancap.Evidence, 0)
	for _, message := range messages {
		if message.Role != llms.ChatMessageTypeTool {
			continue
		}
		for _, part := range message.Parts {
			toolResult, ok := part.(llms.ToolResult)
			if !ok {
				if pointer, pointerOK := part.(*llms.ToolResult); pointerOK && pointer != nil {
					toolResult, ok = *pointer, true
				}
			}
			if !ok {
				continue
			}
			status := "succeeded"
			if toolResult.IsError || toolResult.ErrorMessage != "" {
				status = "failed"
			}
			summary := toolResult.Content
			if summary == "" && toolResult.Value != nil {
				if encoded, err := json.Marshal(toolResult.Value); err == nil {
					summary = string(encoded)
				}
			}
			result = append(result, plancap.Evidence{
				ToolID: toolResult.Name, Status: status, Summary: sanitizeEvidence(summary),
				Error: sanitizeEvidence(toolResult.ErrorMessage), ToolCallID: toolResult.ToolCallID,
			})
		}
	}
	return result
}

func executionCounts(messages []llms.MessageContent) (int, int) {
	modelCalls := 0
	toolCalls := 0
	for _, message := range messages {
		switch message.Role {
		case llms.ChatMessageTypeAI:
			modelCalls++
		case llms.ChatMessageTypeTool:
			for _, part := range message.Parts {
				switch typed := part.(type) {
				case llms.ToolResult:
					toolCalls++
				case *llms.ToolResult:
					if typed != nil {
						toolCalls++
					}
				}
			}
		}
	}
	return modelCalls, toolCalls
}

var (
	sensitiveEvidencePattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+|api[_-]?key\s*[:=]\s*|token\s*[:=]\s*|password\s*[:=]\s*)[^\s,;]+`)
	environmentValuePattern  = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\s*=\s*[^\s,;]+`)
)

func sanitizeEvidence(value string) string {
	value = sensitiveEvidencePattern.ReplaceAllString(value, "[REDACTED]")
	value = environmentValuePattern.ReplaceAllString(value, "[REDACTED_ENV]")
	return limitEvidenceText(value)
}

func limitEvidenceText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 8192 {
		return value[:8192] + "..."
	}
	return value
}

func mergeEvidence(existing, incoming []plancap.Evidence) []plancap.Evidence {
	return mergeEvidenceLimit(existing, incoming, 64)
}

func mergeEvidenceLimit(existing, incoming []plancap.Evidence, limit int) []plancap.Evidence {
	result := append([]plancap.Evidence(nil), existing...)
	for _, item := range incoming {
		duplicate := false
		for _, previous := range result {
			if item.ToolCallID != "" && previous.ToolCallID == item.ToolCallID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, item)
		}
	}
	if limit <= 0 {
		limit = 64
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result
}

func intConfig(values map[string]any, key string) int {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func requiresTestEvidence(criteria []string) bool {
	for _, criterion := range criteria {
		text := strings.ToLower(criterion)
		if strings.Contains(text, "test") || strings.Contains(text, "format") || strings.Contains(text, "验证") {
			return true
		}
	}
	return false
}

func hasTestEvidence(evidence []plancap.Evidence) bool {
	for _, item := range evidence {
		if strings.EqualFold(item.Status, "succeeded") && (strings.Contains(strings.ToLower(item.ToolID), "test") || strings.Contains(strings.ToLower(item.ToolID), "verif") || strings.Contains(strings.ToLower(item.Summary), "test")) {
			return true
		}
	}
	return false
}

func cloneVerifierConfig(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
