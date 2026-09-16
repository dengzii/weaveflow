package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

const defaultPlanClarificationMaxTokens = 700

const defaultPlanClarificationSystemPrompt = `You are the intake component of a plan-mode agent.
Decide whether the objective is sufficiently clear to produce a reliable execution plan.
Return strict JSON only, without markdown fences.

Output shape:
{
  "decision": "ready or needs_clarification",
  "normalized_objective": "a complete objective, or an empty string when clarification is required",
  "question": "one concise question, or an empty string when ready",
  "assumptions": ["an explicit assumption used by the normalized objective"],
  "reason": "a brief explanation of the decision"
}

Rules:
- Use the same language as the objective.
- Ask only when missing information would materially change the plan, evidence selection, permissions, or final conclusion and cannot be obtained with tools.
- Do not ask about minor preferences when a safe conventional default can be stated as an assumption.
- Do not ask for information already present in the objective or clarification answer.
- Ask one directly answerable question containing at most three closely related missing dimensions.
- Never answer the objective or create the execution plan.
- When clarification_answer is non-empty, decision must be ready. Incorporate the answer into normalized_objective and make any remaining non-critical assumptions explicit.
- When ready, normalized_objective must be self-contained and preserve every user constraint.`

type clarificationModelOutput struct {
	Decision            string   `json:"decision"`
	NormalizedObjective string   `json:"normalized_objective"`
	Question            string   `json:"question"`
	Assumptions         []string `json:"assumptions"`
	Reason              string   `json:"reason"`
}

type ClarificationNode struct {
	core.NodeBase
	ModelID               string
	SystemPrompt          string
	MaxTokens             int
	Temperature           float64
	Thinking              llms.ThinkingMode
	ObjectivePath         state.Path
	PendingInputPath      state.Path
	OriginalObjectivePath state.Path
	AnswerPath            state.Path
	AssumptionsPath       state.Path
}

func NewClarificationNode(options ...core.NodeOption) *ClarificationNode {
	target := &ClarificationNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeTypePlanClarification,
			Description: "Clarify a materially ambiguous objective before planning.",
		}),
		SystemPrompt: defaultPlanClarificationSystemPrompt,
		MaxTokens:    defaultPlanClarificationMaxTokens,
	}
	applyNodeOptions(&target.NodeBase, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *ClarificationNode) Validate() error {
	if n == nil {
		return errors.New("plan clarification node is nil")
	}
	if err := n.NodeBase.Validate(); err != nil {
		return err
	}
	if n.ObjectivePath.Empty() || n.PendingInputPath.Empty() || n.OriginalObjectivePath.Empty() || n.AnswerPath.Empty() || n.AssumptionsPath.Empty() {
		return fmt.Errorf("plan clarification node %q requires objective, pending input, original objective, answer, and assumptions paths", n.ID())
	}
	if n.MaxTokens <= 0 || n.Temperature < 0 || n.Temperature > 2 {
		return fmt.Errorf("plan clarification node %q has invalid model budget or temperature", n.ID())
	}
	if !validPlanThinkingMode(n.Thinking) {
		return fmt.Errorf("plan clarification node %q has invalid thinking mode %q", n.ID(), n.Thinking)
	}
	return nil
}

func (n *ClarificationNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.NodeBase, NodeTypePlanClarification, map[string]any{
		"model_id":      n.ModelID,
		"system_prompt": n.SystemPrompt,
		"max_tokens":    n.MaxTokens,
		"temperature":   n.Temperature,
		"thinking":      string(n.Thinking),
	}, map[string]state.Path{
		"objective":          n.ObjectivePath,
		"pending_input":      n.PendingInputPath,
		"original_objective": n.OriginalObjectivePath,
		"answer":             n.AnswerPath,
		"assumptions":        n.AssumptionsPath,
	})
}

func ClarificationNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypePlanClarification,
			Title:       "Plan Clarification",
			Description: "Clarify a materially ambiguous objective before generating a plan.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"system_prompt": dsl.JSONSchema{
						"type": "string", "title": "System Prompt", "x-control": "textarea",
					},
					"max_tokens": dsl.JSONSchema{
						"type": "integer", "title": "Maximum Output Tokens", "minimum": 1, "default": defaultPlanClarificationMaxTokens,
					},
					"temperature": dsl.JSONSchema{
						"type": "number", "title": "Temperature", "minimum": 0, "maximum": 2, "default": 0,
					},
					"thinking": dsl.JSONSchema{
						"type": "string", "title": "Reasoning Effort",
						"enum": []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"},
					},
				},
				"additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				clarificationStringPort("objective", "Objective inspected and normalized before planning.", "shared.request.input", dsl.StateAccessReadWrite, false),
				clarificationStringPort("pending_input", "Clarification answer supplied when resuming the paused run.", "shared.request.pending_input", dsl.StateAccessReadWrite, false),
				clarificationStringPort("original_objective", "Original objective retained for auditability.", "shared.plan_intake.original_objective", dsl.StateAccessWrite, false),
				clarificationStringPort("answer", "User clarification retained for auditability.", "shared.plan_intake.answer", dsl.StateAccessWrite, false),
				{
					Name: "assumptions", Description: "Explicit assumptions used by the normalized objective.",
					DefaultPath: "shared.plan_intake.assumptions", Required: false,
					Schema: dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					Mode:   dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace,
				},
			},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			objectivePath, err := resolvedPath(resolved, "objective")
			if err != nil {
				return nil, err
			}
			pendingInputPath, err := resolvedPath(resolved, "pending_input")
			if err != nil {
				return nil, err
			}
			originalObjectivePath, err := resolvedPath(resolved, "original_objective")
			if err != nil {
				return nil, err
			}
			answerPath, err := resolvedPath(resolved, "answer")
			if err != nil {
				return nil, err
			}
			assumptionsPath, err := resolvedPath(resolved, "assumptions")
			if err != nil {
				return nil, err
			}
			target := NewClarificationNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			target.ModelID = config.String(spec.Config, "model_id")
			if _, exists := spec.Config["system_prompt"]; exists {
				target.SystemPrompt = config.String(spec.Config, "system_prompt")
			}
			if value, ok := config.Int(spec.Config, "max_tokens"); ok {
				target.MaxTokens = value
			}
			if value, ok := config.Float(spec.Config, "temperature"); ok {
				target.Temperature = value
			}
			target.Thinking = llms.ThinkingMode(config.String(spec.Config, "thinking"))
			target.ObjectivePath = objectivePath
			target.PendingInputPath = pendingInputPath
			target.OriginalObjectivePath = originalObjectivePath
			target.AnswerPath = answerPath
			target.AssumptionsPath = assumptionsPath
			if err := target.Validate(); err != nil {
				return nil, err
			}
			return target, nil
		},
	}
}

func clarificationStringPort(name, description, defaultPath string, mode dsl.StateAccessMode, required bool) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name: name, Description: description, DefaultPath: defaultPath, Required: required,
		Schema: dsl.JSONSchema{"type": "string"}, Mode: mode, MergeStrategy: dsl.StateMergeReplace,
	}
}

func (n *ClarificationNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *ClarificationNode) execute(ctx core.Context, access *state.Access) error {
	objective, err := requiredClarificationText(access, n.ObjectivePath, "objective")
	if err != nil {
		return err
	}
	answer, hasAnswer, err := optionalClarificationText(access, n.PendingInputPath)
	if err != nil {
		return err
	}

	model := ctx.Model(n.ModelID)
	if model == nil {
		return fmt.Errorf("plan clarification node: model %q not available", effectiveModelID(n.ModelID))
	}
	payload := map[string]any{
		"objective":                      objective,
		"clarification_answer":           answer,
		"clarification_already_answered": hasAnswer,
	}
	prompt, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("plan clarification node: encode prompt: %w", err)
	}
	temperature := n.Temperature
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		ModelID: effectiveModelID(n.ModelID),
		Mode:    llms.ModelModeChat,
		Messages: []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveSystemPrompt()),
			llms.TextParts(llms.ChatMessageTypeHuman, "Assess this objective from the JSON payload:\n\n"+string(prompt)),
		},
		Thinking:       n.effectiveThinking(),
		Temperature:    &temperature,
		MaxTokens:      n.MaxTokens,
		ResponseName:   "plan_clarification",
		ResponseSchema: clarificationOutputSchema(),
		StrictResponse: true,
	})
	if err != nil {
		return fmt.Errorf("plan clarification node: assess objective: %w", err)
	}
	if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
		return errors.New("plan clarification node: model returned no choices")
	}
	output, err := parseClarificationOutput(response.Choices[0].Content)
	if err != nil {
		return fmt.Errorf("plan clarification node: parse response: %w", err)
	}

	decision := strings.ToLower(strings.TrimSpace(output.Decision))
	if !hasAnswer {
		switch decision {
		case ClarificationDecisionReady:
			if strings.TrimSpace(output.NormalizedObjective) == "" {
				return errors.New("plan clarification node: ready decision requires normalized_objective")
			}
		case ClarificationDecisionNeedsInput:
			question := strings.TrimSpace(output.Question)
			if question == "" {
				return errors.New("plan clarification node: clarification decision requires question")
			}
			return &core.NodeInterrupt{NodeID: n.ID(), Value: textLimit(question, 1200)}
		default:
			return fmt.Errorf("plan clarification node: unknown decision %q", output.Decision)
		}
	}

	normalizedObjective := strings.TrimSpace(output.NormalizedObjective)
	if hasAnswer && (decision != ClarificationDecisionReady || normalizedObjective == "") {
		normalizedObjective = objective + "\n\nUser clarification:\n" + answer
	}
	if normalizedObjective == "" {
		return errors.New("plan clarification node: normalized objective is empty")
	}
	if err := access.SetAny(n.OriginalObjectivePath, objective); err != nil {
		return err
	}
	if hasAnswer {
		if err := access.SetAny(n.AnswerPath, answer); err != nil {
			return err
		}
	}
	if err := access.SetAny(n.AssumptionsPath, normalizeClarificationAssumptions(output.Assumptions)); err != nil {
		return err
	}
	if err := access.SetAny(n.ObjectivePath, normalizedObjective); err != nil {
		return err
	}
	if hasAnswer {
		if err := access.Delete(n.PendingInputPath); err != nil {
			return err
		}
	}
	return nil
}

func (n *ClarificationNode) effectiveSystemPrompt() string {
	if n == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return defaultPlanClarificationSystemPrompt
	}
	return n.SystemPrompt
}

func (n *ClarificationNode) effectiveThinking() llms.ThinkingMode {
	if n == nil || n.Thinking == "" {
		return llms.ThinkingModeLow
	}
	return n.Thinking
}

func clarificationOutputSchema() state.JSONSchema {
	return state.JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"decision":             map[string]any{"type": "string", "enum": []string{ClarificationDecisionReady, ClarificationDecisionNeedsInput}},
			"normalized_objective": map[string]any{"type": "string"},
			"question":             map[string]any{"type": "string"},
			"assumptions": map[string]any{
				"type": "array", "maxItems": 8, "items": map[string]any{"type": "string"},
			},
			"reason": map[string]any{"type": "string"},
		},
		"required":             []string{"decision", "normalized_objective", "question", "assumptions", "reason"},
		"additionalProperties": false,
	}
}

func parseClarificationOutput(content string) (clarificationModelOutput, error) {
	content = strings.TrimSpace(stripPlanJSONFence(content))
	if content == "" {
		return clarificationModelOutput{}, errors.New("empty content")
	}
	var output clarificationModelOutput
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return clarificationModelOutput{}, err
	}
	return output, nil
}

func requiredClarificationText(access *state.Access, path state.Path, name string) (string, error) {
	value, exists := access.ReadAny(path)
	if !exists {
		return "", fmt.Errorf("plan clarification node: %s is required", name)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("plan clarification node: %s path %q must be string, got %T", name, path.String(), value)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("plan clarification node: %s is required", name)
	}
	return text, nil
}

func optionalClarificationText(access *state.Access, path state.Path) (string, bool, error) {
	value, exists := access.ReadAny(path)
	if !exists || value == nil {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("plan clarification node: pending input path %q must be string, got %T", path.String(), value)
	}
	text = strings.TrimSpace(text)
	return text, text != "", nil
}

func normalizeClarificationAssumptions(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	if result == nil {
		return []string{}
	}
	return result
}
