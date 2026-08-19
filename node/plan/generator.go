package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	executioncap "github.com/dengzii/weaveflow/capability/execution"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

const (
	defaultPlanMaxSteps   = 6
	defaultPlanMaxReplans = 1
)

const defaultPlanGeneratorSystemPrompt = `You are the planning component of a plan-mode agent.
Return strict JSON only, without markdown fences.

The input contains an objective, available tools, and optional results from a previous plan. Produce the smallest reliable execution plan that can satisfy the objective.

Output shape:
{
  "summary": "one sentence describing the approach",
  "steps": [
    {
      "id": "step_1",
      "title": "short title",
      "description": "a concrete instruction for the executor",
      "tool_ids": ["optional_tool_id"]
    }
  ]
}

Rules:
- Use the same language as the objective.
- Each step must have one verifiable deliverable.
- Use as few sequential steps as accuracy allows.
- Only reference tool IDs present in available_tools.
- Do not add a final synthesis step; final synthesis is handled separately.
- When replanning, preserve successful information and replace only the ineffective path.`

type GeneratorNode struct {
	core.NodeBase
	ModelID       string
	ToolIDs       []string
	SystemPrompt  string
	MaxSteps      int
	MaxReplans    int
	ObjectivePath state.Path
	PlanPath      state.Path
	ExecutionPath state.Path
}

func NewGeneratorNode(options ...core.NodeOption) *GeneratorNode {
	target := &GeneratorNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeTypePlanGenerator,
			Description: "Generate or revise a structured execution plan.",
		}),
		SystemPrompt: defaultPlanGeneratorSystemPrompt,
		MaxSteps:     defaultPlanMaxSteps,
		MaxReplans:   defaultPlanMaxReplans,
	}
	applyNodeOptions(&target.NodeBase, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *GeneratorNode) Validate() error {
	if n == nil {
		return errors.New("plan generator node is nil")
	}
	if err := n.NodeBase.Validate(); err != nil {
		return err
	}
	if n.ObjectivePath.Empty() || n.PlanPath.Empty() || n.ExecutionPath.Empty() {
		return fmt.Errorf("plan generator node %q requires objective, plan, and execution paths", n.ID())
	}
	return nil
}

func (n *GeneratorNode) GraphNodeSpec() dsl.GraphNodeSpec {
	nodeConfig := map[string]any{
		"model_id":      n.ModelID,
		"tool_ids":      n.ToolIDs,
		"system_prompt": n.SystemPrompt,
		"max_steps":     n.MaxSteps,
		"max_replans":   n.MaxReplans,
	}
	return newGraphNodeSpec(n.NodeBase, NodeTypePlanGenerator, nodeConfig, map[string]state.Path{
		"objective": n.ObjectivePath, "plan": n.PlanPath, "execution": n.ExecutionPath,
	})
}

func GeneratorNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypePlanGenerator,
			Title:       "Plan Generator",
			Description: "Generate or revise a structured execution plan.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"tool_ids": dsl.JSONSchema{
						"type": "array", "title": "Tools", "items": dsl.JSONSchema{"type": "string"},
						"description": "Tools the planner may assign to generated plan steps.",
					},
					"system_prompt": dsl.JSONSchema{
						"type":      "string",
						"title":     "System Prompt",
						"x-control": "textarea",
					},
					"max_steps": dsl.JSONSchema{
						"type": "integer", "title": "Max Plan Steps", "minimum": 1,
						"description": "Maximum number of steps retained in each generated plan.",
					},
					"max_replans": dsl.JSONSchema{
						"type": "integer", "title": "Max Replans", "minimum": 0,
						"description": "Maximum number of plan revisions allowed after execution requests a replan.",
					},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			primitivePort("objective", "Objective to turn into an execution plan.", "string", dsl.StateAccessRead, true),
			capabilityPort("plan", "Structured plan and replan state.", plancap.CapabilityID, true,
				capabilityField(plancap.FieldObjective, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldStatus, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldSummary, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldSteps, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldHistory, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldCurrentIndex, dsl.StateAccessWrite),
				capabilityField(plancap.FieldReplanCount, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldMaxReplans, dsl.StateAccessWrite),
				capabilityField(plancap.FieldReplanReason, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldFinalAnswer, dsl.StateAccessWrite)),
			capabilityPort("execution", "Current plan step execution state.", executioncap.CapabilityID, true,
				capabilityField(executioncap.FieldCurrentStep, dsl.StateAccessWrite),
				capabilityField(executioncap.FieldLastLLMStep, dsl.StateAccessWrite)),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			objectivePath, err := resolvedPath(resolved, "objective")
			if err != nil {
				return nil, err
			}
			planPath, err := resolvedPath(resolved, "plan")
			if err != nil {
				return nil, err
			}
			executionPath, err := resolvedPath(resolved, "execution")
			if err != nil {
				return nil, err
			}
			target := NewGeneratorNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			target.ModelID = config.String(spec.Config, "model_id")
			target.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			if _, exists := spec.Config["system_prompt"]; exists {
				target.SystemPrompt = config.String(spec.Config, "system_prompt")
			}
			if value, ok := config.Int(spec.Config, "max_steps"); ok {
				target.MaxSteps = value
			}
			if value, ok := config.Int(spec.Config, "max_replans"); ok {
				target.MaxReplans = value
			}
			if target.MaxSteps <= 0 {
				return nil, fmt.Errorf("build plan generator node %q: max_steps must be greater than 0", spec.ID)
			}
			if target.MaxReplans < 0 {
				return nil, fmt.Errorf("build plan generator node %q: max_replans must not be negative", spec.ID)
			}
			target.ObjectivePath = objectivePath
			target.PlanPath = planPath
			target.ExecutionPath = executionPath
			return target, nil
		},
	}
}

func (n *GeneratorNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *GeneratorNode) execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model(n.ModelID)
	if model == nil {
		return fmt.Errorf("plan generator node: model %q not available", effectiveModelID(n.ModelID))
	}
	planner, err := plancap.Bind(access, n.PlanPath)
	if err != nil {
		return err
	}
	execution, err := executioncap.Bind(access, n.ExecutionPath)
	if err != nil {
		return err
	}
	objectiveInput, err := state.Get(access, state.NewRef[string](n.ObjectivePath))
	if err != nil {
		return err
	}
	current := planner.Value()
	objective := stringValue(current[planFieldObjective])
	if objective == "" {
		objective = strings.TrimSpace(objectiveInput)
	}
	if objective == "" {
		return errors.New("plan generator node: objective is required in planner.objective or request.input")
	}
	previousSteps := stepsFromValue(current[planFieldSteps])
	isReplan := strings.EqualFold(stringValue(current[planFieldStatus]), PlanStatusReplan)
	replanCount := intValue(current[planFieldReplanCount])
	maxReplans := n.effectiveMaxReplans()
	if isReplan && replanCount >= maxReplans {
		return fmt.Errorf("plan generator node: maximum replans reached (%d)", maxReplans)
	}

	availableTools := ctx.FilterTools(n.ToolIDs)
	payload := map[string]any{
		"objective":        objective,
		"is_replan":        isReplan,
		"replan_reason":    stringValue(current[planFieldReplanReason]),
		"previous_summary": stringValue(current[planFieldSummary]),
		"previous_steps":   stepMaps(previousSteps),
		"available_tools":  toolDescriptions(availableTools),
	}
	prompt, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("plan generator node: encode prompt: %w", err)
	}
	temperature := 0.2
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		ModelID: effectiveModelID(n.ModelID),
		Mode:    llms.ModelModeChat,
		Messages: []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveSystemPrompt()),
			llms.TextParts(llms.ChatMessageTypeHuman, "Create the execution plan from this JSON payload:\n\n"+string(prompt)),
		},
		Thinking:       llms.ThinkingModeNone,
		Temperature:    &temperature,
		ResponseName:   "execution_plan",
		ResponseSchema: modelOutputSchema(),
		StrictResponse: true,
	})
	if err != nil {
		return fmt.Errorf("plan generator node: generate plan: %w", err)
	}
	if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
		return errors.New("plan generator node: model returned no choices")
	}
	parsed, err := parsePlanModelOutput(response.Choices[0].Content)
	if err != nil {
		return fmt.Errorf("plan generator node: parse response: %w", err)
	}
	steps := normalizePlanSteps(parsed.Steps, n.effectiveMaxSteps(), toolNames(availableTools))
	if len(steps) == 0 {
		return errors.New("plan generator node: model produced no valid steps")
	}

	history := mapSlice(current[planFieldHistory])
	if isReplan && len(previousSteps) > 0 {
		history = append(history, map[string]any{
			"summary": stringValue(current[planFieldSummary]),
			"reason":  stringValue(current[planFieldReplanReason]),
			"steps":   stepMaps(previousSteps),
		})
		replanCount++
	}
	if err := planner.Merge(map[string]any{
		planFieldObjective:    objective,
		planFieldStatus:       PlanStatusExecuting,
		planFieldSummary:      strings.TrimSpace(parsed.Summary),
		planFieldSteps:        stepMaps(steps),
		planFieldHistory:      history,
		planFieldCurrentIndex: 0,
		planFieldReplanCount:  replanCount,
		planFieldMaxReplans:   maxReplans,
	}); err != nil {
		return err
	}
	if err := planner.DeleteField(planFieldReplanReason); err != nil {
		return err
	}
	if err := planner.DeleteField(planFieldFinalAnswer); err != nil {
		return err
	}
	if err := execution.SetCurrentStep(map[string]any{}); err != nil {
		return err
	}
	if err := execution.SetLastLLMStepID(""); err != nil {
		return err
	}
	return nil
}

func (n *GeneratorNode) effectiveSystemPrompt() string {
	if n == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return defaultPlanGeneratorSystemPrompt
	}
	return n.SystemPrompt
}

func (n *GeneratorNode) effectiveMaxSteps() int {
	if n == nil || n.MaxSteps <= 0 {
		return defaultPlanMaxSteps
	}
	return n.MaxSteps
}

func (n *GeneratorNode) effectiveMaxReplans() int {
	if n == nil || n.MaxReplans < 0 {
		return defaultPlanMaxReplans
	}
	return n.MaxReplans
}
