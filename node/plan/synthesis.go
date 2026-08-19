package plan

import (
	"errors"
	"fmt"
	"strings"

	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

const defaultPlanSynthesisSystemPrompt = `Synthesize the final user-facing answer from the objective and plan step results.
Use successful evidence, acknowledge material failures when necessary, and do not invent missing facts.
Answer directly in the same language as the objective.`

type SynthesisNode struct {
	core.NodeBase
	ModelID      string
	SystemPrompt string
	PlanPath     state.Path
	ResultPath   state.Path
}

func NewSynthesisNode(options ...core.NodeOption) *SynthesisNode {
	target := &SynthesisNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeTypePlanSynthesis,
			Description: "Synthesize plan results into the final answer.",
		}),
		SystemPrompt: defaultPlanSynthesisSystemPrompt,
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
	return nil
}

func (n *SynthesisNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.NodeBase, NodeTypePlanSynthesis, map[string]any{
		"model_id":      n.ModelID,
		"system_prompt": n.SystemPrompt,
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
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("plan", "Plan results and final status.", plancap.CapabilityID, true,
				capabilityField(plancap.FieldObjective, dsl.StateAccessRead),
				capabilityField(plancap.FieldStatus, dsl.StateAccessWrite),
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
	model := ctx.Model(n.ModelID)
	if model == nil {
		return fmt.Errorf("plan synthesis node: model %q not available", effectiveModelID(n.ModelID))
	}
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

	temperature := 0.2
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		ModelID: effectiveModelID(n.ModelID),
		Mode:    llms.ModelModeChat,
		Messages: []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveSystemPrompt()),
			llms.TextParts(llms.ChatMessageTypeHuman, buildPlanSynthesisPrompt(objective, stringValue(planValue[planFieldSummary]), steps)),
		},
		Thinking:    llms.ThinkingModeLow,
		Temperature: &temperature,
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
	if err := state.Replace(access, state.NewRef[string](n.ResultPath), answer); err != nil {
		return err
	}
	if err := planner.SetField(planFieldFinalAnswer, answer); err != nil {
		return err
	}
	return planner.SetField(planFieldStatus, PlanStatusDone)
}

func (n *SynthesisNode) effectiveSystemPrompt() string {
	if n == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return defaultPlanSynthesisSystemPrompt
	}
	return n.SystemPrompt
}

func buildPlanSynthesisPrompt(objective string, summary string, steps []plancap.Step) string {
	var builder strings.Builder
	builder.WriteString("Objective:\n")
	builder.WriteString(objective)
	if summary != "" {
		builder.WriteString("\n\nPlan summary:\n")
		builder.WriteString(summary)
	}
	builder.WriteString("\n\nStep results:\n")
	for _, step := range steps {
		fmt.Fprintf(&builder, "- [%s] %s\n  status: %s\n", step.ID, step.Title, step.Status)
		if step.Result != "" {
			fmt.Fprintf(&builder, "  result: %s\n", textLimit(step.Result, 6000))
		}
		if step.Error != "" {
			fmt.Fprintf(&builder, "  error: %s\n", textLimit(step.Error, 1500))
		}
	}
	builder.WriteString("\nProduce the final answer now.")
	return builder.String()
}
