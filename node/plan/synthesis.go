package plan

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

const defaultPlanSynthesisSystemPrompt = `Synthesize the final user-facing answer from the objective and plan step results.
Use successful evidence, acknowledge material failures when necessary, and do not invent missing facts.
Use only persisted source URLs and system-provided access timestamps; never invent publication or access dates.
Answer directly in the same language as the objective.`

type SynthesisNode struct {
	core.NodeBase
	ModelID             string
	SystemPrompt        string
	RequireEvidenceRefs bool
	FailOnIncomplete    bool
	MaxTokens           int
	Temperature         float64
	Thinking            llms.ThinkingMode
	PlanPath            state.Path
	ResultPath          state.Path
}

func NewSynthesisNode(options ...core.NodeOption) *SynthesisNode {
	target := &SynthesisNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeTypePlanSynthesis,
			Description: "Synthesize plan results into the final answer.",
		}),
		SystemPrompt:     defaultPlanSynthesisSystemPrompt,
		FailOnIncomplete: true,
	}
	applyNodeOptions(&target.NodeBase, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *SynthesisNode) Validate() error {
	if n == nil {
		return errors.New("plan synthesis node is nil")
	}
	if err := n.NodeBase.Validate(); err != nil {
		return err
	}
	if n.PlanPath.Empty() || n.ResultPath.Empty() {
		return fmt.Errorf("plan synthesis node %q requires plan and result paths", n.ID())
	}
	if n.MaxTokens < 0 || n.Temperature < 0 || n.Temperature > 2 {
		return fmt.Errorf("plan synthesis node %q has invalid model budget or temperature", n.ID())
	}
	if !validPlanThinkingMode(n.Thinking) {
		return fmt.Errorf("plan synthesis node %q has invalid thinking mode %q", n.ID(), n.Thinking)
	}
	return nil
}

func (n *SynthesisNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.NodeBase, NodeTypePlanSynthesis, map[string]any{
		"model_id":              n.ModelID,
		"system_prompt":         n.SystemPrompt,
		"require_evidence_refs": n.RequireEvidenceRefs,
		"fail_on_incomplete":    n.FailOnIncomplete,
		"max_tokens":            n.MaxTokens,
		"temperature":           n.Temperature,
		"thinking":              string(n.Thinking),
	}, map[string]state.Path{"plan": n.PlanPath, "result": n.ResultPath})
}

func SynthesisNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypePlanSynthesis,
			Title:       "Plan Synthesis",
			Description: "Synthesize plan results into the final answer.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"system_prompt": dsl.JSONSchema{
						"type":      "string",
						"title":     "System Prompt",
						"x-control": "textarea",
					},
					"require_evidence_refs": dsl.JSONSchema{
						"type": "boolean", "title": "Require Evidence References",
						"description": "Require final answers to include stable references to successful step evidence.",
					},
					"fail_on_incomplete": dsl.JSONSchema{
						"type": "boolean", "title": "Fail On Incomplete Plan",
						"description": "Mark the plan failed when any step is not verified.", "default": true,
					},
					"max_tokens": dsl.JSONSchema{
						"type": "integer", "title": "Synthesis Max Output Tokens", "minimum": 1,
					},
					"temperature": dsl.JSONSchema{
						"type": "number", "title": "Synthesis Temperature", "minimum": 0, "maximum": 2,
					},
					"thinking": dsl.JSONSchema{
						"type": "string", "title": "Synthesis Reasoning Effort",
						"enum": []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"},
					},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("plan", "Plan results and final status.", plancap.CapabilityID, true,
				capabilityField(plancap.FieldObjective, dsl.StateAccessRead),
				capabilityField(plancap.FieldStatus, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldSummary, dsl.StateAccessRead),
				capabilityField(plancap.FieldSteps, dsl.StateAccessRead),
				capabilityField(plancap.FieldFinalAnswer, dsl.StateAccessWrite)),
			primitivePort("result", "Final synthesized answer.", "string", dsl.StateAccessWrite, true),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			planPath, err := resolvedPath(resolved, "plan")
			if err != nil {
				return nil, err
			}
			resultPath, err := resolvedPath(resolved, "result")
			if err != nil {
				return nil, err
			}
			target := NewSynthesisNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			target.ModelID = config.String(spec.Config, "model_id")
			if _, exists := spec.Config["system_prompt"]; exists {
				target.SystemPrompt = config.String(spec.Config, "system_prompt")
			}
			if value, ok := config.Bool(spec.Config, "require_evidence_refs"); ok {
				target.RequireEvidenceRefs = value
			}
			if value, ok := config.Bool(spec.Config, "fail_on_incomplete"); ok {
				target.FailOnIncomplete = value
			}
			if value, ok := config.Int(spec.Config, "max_tokens"); ok {
				target.MaxTokens = value
			}
			if value, ok := config.Float(spec.Config, "temperature"); ok {
				target.Temperature = value
			}
			target.Thinking = llms.ThinkingMode(config.String(spec.Config, "thinking"))
			if !validPlanThinkingMode(target.Thinking) {
				return nil, fmt.Errorf("build plan synthesis node %q: invalid thinking mode %q", spec.ID, target.Thinking)
			}
			target.PlanPath = planPath
			target.ResultPath = resultPath
			return target, nil
		},
	}
}

func (n *SynthesisNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *SynthesisNode) execute(ctx core.Context, access *state.Access) error {
	planner, err := plancap.Bind(access, n.PlanPath)
	if err != nil {
		return err
	}
	planValue := planner.Value()
	objective := stringValue(planValue[planFieldObjective])
	steps := stepsFromValue(planValue[planFieldSteps])
	if objective == "" {
		return errors.New("plan synthesis node: objective is empty")
	}
	if status := stringValue(planValue[planFieldStatus]); status != PlanStatusFinalizing {
		return fmt.Errorf("plan synthesis node: plan status %q is not ready for synthesis", status)
	}
	if n.FailOnIncomplete {
		if reason := incompletePlanReason(steps); reason != "" {
			if err := planner.SetField(planFieldStatus, PlanStatusFailed); err != nil {
				return err
			}
			return fmt.Errorf("plan synthesis node: refusing incomplete plan: %s", reason)
		}
	}

	model := ctx.Model(n.ModelID)
	if model == nil {
		return fmt.Errorf("plan synthesis node: model %q not available", effectiveModelID(n.ModelID))
	}
	temperature := n.effectiveTemperature()
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		ModelID: effectiveModelID(n.ModelID),
		Mode:    llms.ModelModeChat,
		Messages: []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveSystemPrompt()),
			llms.TextParts(llms.ChatMessageTypeHuman, buildPlanSynthesisPrompt(objective, stringValue(planValue[planFieldSummary]), steps, n.RequireEvidenceRefs)),
		},
		Temperature: &temperature,
		MaxTokens:   n.MaxTokens,
		Thinking:    n.effectiveThinking(),
	})
	if err != nil {
		return fmt.Errorf("plan synthesis node: synthesize answer: %w", err)
	}
	if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
		return errors.New("plan synthesis node: model returned no choices")
	}
	answer := strings.TrimSpace(response.Choices[0].Content)
	if answer == "" {
		return errors.New("plan synthesis node: model returned an empty answer")
	}
	if n.RequireEvidenceRefs {
		answer, err = ensureFinalEvidenceReferences(answer, steps)
		if err != nil {
			return fmt.Errorf("plan synthesis node: %w", err)
		}
	}
	if err := state.Replace(access, state.NewRef[string](n.ResultPath), answer); err != nil {
		return err
	}
	if err := planner.SetField(planFieldFinalAnswer, answer); err != nil {
		return err
	}
	return planner.SetField(planFieldStatus, PlanStatusDone)
}

func (n *SynthesisNode) effectiveTemperature() float64 {
	if n == nil || n.Temperature <= 0 {
		return 0.2
	}
	return n.Temperature
}

func (n *SynthesisNode) effectiveThinking() llms.ThinkingMode {
	if n == nil || n.Thinking == "" {
		return llms.ThinkingModeLow
	}
	return n.Thinking
}

func (n *SynthesisNode) effectiveSystemPrompt() string {
	if n == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return defaultPlanSynthesisSystemPrompt
	}
	return n.SystemPrompt
}

func buildPlanSynthesisPrompt(objective string, summary string, steps []plancap.Step, requireEvidenceRefs bool) string {
	var builder strings.Builder
	builder.WriteString("Objective:\n")
	builder.WriteString(objective)
	if summary != "" {
		builder.WriteString("\n\nPlan summary:\n")
		builder.WriteString(summary)
	}
	builder.WriteString("\n\nStep results:\n")
	for stepIndex, step := range steps {
		fmt.Fprintf(&builder, "- [S%d] [%s] %s\n  status: %s\n", stepIndex+1, step.ID, step.Title, step.Status)
		if step.Result != "" {
			fmt.Fprintf(&builder, "  result: %s\n", textLimit(step.Result, 6000))
		}
		if step.Error != "" {
			fmt.Fprintf(&builder, "  error: %s\n", textLimit(step.Error, 1500))
		}
		fmt.Fprintf(&builder, "  verification: %s - %s\n", step.VerificationStatus, textLimit(step.VerificationSummary, 1500))
		for evidenceIndex, evidence := range step.Evidence {
			fmt.Fprintf(&builder, "  evidence [S%d:E%d]: %s %s - %s", stepIndex+1, evidenceIndex+1, evidence.ToolID, evidence.Status, textLimit(evidence.Summary, 1000))
			if evidence.URL != "" {
				fmt.Fprintf(&builder, " | url=%s", evidence.URL)
			}
			if evidence.HTTPStatus != 0 {
				fmt.Fprintf(&builder, " | http_status=%d", evidence.HTTPStatus)
			}
			if evidence.AccessedAt != "" {
				fmt.Fprintf(&builder, " | accessed_at=%s", evidence.AccessedAt)
			}
			builder.WriteByte('\n')
		}
	}
	if requireEvidenceRefs {
		builder.WriteString("\nEvery material factual claim must cite one or more listed evidence refs such as [S1:E1]. Do not cite nonexistent refs.")
	}
	builder.WriteString("\nProduce the final answer now.")
	return builder.String()
}

func ensureFinalEvidenceReferences(answer string, steps []plancap.Step) (string, error) {
	refs := successfulEvidenceReferences(steps)
	if len(refs) == 0 {
		return "", errors.New("final answer requires evidence refs but no successful evidence exists")
	}
	allEvidence := make(map[string]plancap.Evidence)
	for stepIndex, step := range steps {
		for evidenceIndex, evidence := range step.Evidence {
			allEvidence[fmt.Sprintf("[S%d:E%d]", stepIndex+1, evidenceIndex+1)] = evidence
		}
	}
	for _, rawRef := range evidenceReferencePattern.FindAllString(answer, -1) {
		evidence, ok := allEvidence[rawRef]
		if !ok || !evidenceIsSuccessful(evidence) {
			return "", fmt.Errorf("final answer references invalid evidence %s", rawRef)
		}
	}
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(answer))
	builder.WriteString("\n\nEvidence references:\n")
	for _, ref := range refs {
		evidence := allEvidence[ref]
		fmt.Fprintf(&builder, "- %s %s", ref, evidenceReferenceDetails(evidence))
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String()), nil
}

func successfulEvidenceReferences(steps []plancap.Step) []string {
	refs := make([]string, 0, 12)
	for stepIndex, step := range steps {
		for evidenceIndex, evidence := range step.Evidence {
			if !evidenceIsSuccessful(evidence) {
				continue
			}
			refs = append(refs, fmt.Sprintf("[S%d:E%d]", stepIndex+1, evidenceIndex+1))
			if len(refs) == 12 {
				return refs
			}
		}
	}
	return refs
}

var evidenceReferencePattern = regexp.MustCompile(`\[S[0-9]+:E[0-9]+\]`)

func evidenceIsSuccessful(evidence plancap.Evidence) bool {
	return strings.EqualFold(strings.TrimSpace(evidence.Status), "succeeded") && evidence.Error == "" && validHTTPStatus(evidence.HTTPStatus)
}

func evidenceReferenceDetails(evidence plancap.Evidence) string {
	parts := []string{evidence.ToolID}
	if evidence.Title != "" {
		parts = append(parts, evidence.Title)
	}
	if evidence.URL != "" {
		parts = append(parts, evidence.URL)
	}
	if evidence.HTTPStatus != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", evidence.HTTPStatus))
	}
	if evidence.AccessedAt != "" {
		parts = append(parts, "accessed "+evidence.AccessedAt)
	}
	return strings.Join(parts, " | ")
}

func incompletePlanReason(steps []plancap.Step) string {
	if len(steps) == 0 {
		return "plan has no steps"
	}
	for _, step := range steps {
		if step.Status != PlanStepStatusDone || step.VerificationStatus != VerificationStatusPassed {
			return fmt.Sprintf("step %q is %s/%s", step.ID, step.Status, step.VerificationStatus)
		}
	}
	return ""
}
