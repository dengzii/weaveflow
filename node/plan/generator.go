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
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
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
      "tool_ids": ["optional_tool_id"],
      "deliverables": ["a concrete observable artifact"],
      "acceptance_criteria": ["a condition that can be checked as true or false"],
      "verification_strategy": "evidence or the configured deterministic verifier ID"
    }
  ]
}

Rules:
- Use the same language as the objective.
- Each step must have at least one observable deliverable and one falsifiable acceptance criterion.
- Choose verification_strategy "evidence" for inspection or implementation work, and the configured deterministic verifier ID for test/format/file checks.
- For implementation objectives, include explicit inspect, implement, test, and fix/verify work.
- Use as few sequential steps as accuracy allows.
- Only reference tool IDs present in available_tools.
- Do not add a final synthesis step; final synthesis is handled separately.
- When replanning, preserve successful information and replace only the ineffective path.
- Treat a claim that tests pass as unverified until the worker actually runs the focused test command.`

type GeneratorNode struct {
	core.NodeBase
	ModelID                     string
	ToolIDs                     []string
	VerifierID                  string
	DefaultVerificationStrategy string
	SystemPrompt                string
	MaxSteps                    int
	MaxReplans                  int
	MaxTokens                   int
	Temperature                 float64
	Thinking                    llms.ThinkingMode
	ObjectivePath               state.Path
	PlanPath                    state.Path
	ExecutionPath               state.Path
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
	if n.MaxSteps <= 0 || n.MaxReplans < 0 || n.MaxTokens < 0 || n.Temperature < 0 || n.Temperature > 2 {
		return fmt.Errorf("plan generator node %q has invalid budget or temperature", n.ID())
	}
	if !validPlanThinkingMode(n.Thinking) {
		return fmt.Errorf("plan generator node %q has invalid thinking mode %q", n.ID(), n.Thinking)
	}
	return nil
}

func (n *GeneratorNode) GraphNodeSpec() dsl.GraphNodeSpec {
	nodeConfig := map[string]any{
		"model_id":              n.ModelID,
		"tool_ids":              n.ToolIDs,
		"verifier_id":           n.VerifierID,
		"verification_strategy": n.DefaultVerificationStrategy,
		"system_prompt":         n.SystemPrompt,
		"max_steps":             n.MaxSteps,
		"max_replans":           n.MaxReplans,
		"max_tokens":            n.MaxTokens,
		"temperature":           n.Temperature,
		"thinking":              string(n.Thinking),
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
					"verifier_id": dsl.JSONSchema{
						"type": "string", "title": "Verifier ID",
						"description": "Deterministic verifier assigned to the final plan step.",
					},
					"verification_strategy": dsl.JSONSchema{
						"type": "string", "title": "Default Verification Strategy",
						"description": "Strategy forced onto every generated step; leave empty to honor model-selected strategies.",
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
					"max_tokens": dsl.JSONSchema{
						"type": "integer", "title": "Planner Max Output Tokens", "minimum": 1,
					},
					"temperature": dsl.JSONSchema{
						"type": "number", "title": "Planner Temperature", "minimum": 0, "maximum": 2,
					},
					"thinking": dsl.JSONSchema{
						"type": "string", "title": "Planner Reasoning Effort",
						"enum": []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"},
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
			target.VerifierID = config.String(spec.Config, "verifier_id")
			target.DefaultVerificationStrategy = config.String(spec.Config, "verification_strategy")
			if _, exists := spec.Config["system_prompt"]; exists {
				target.SystemPrompt = config.String(spec.Config, "system_prompt")
			}
			if value, ok := config.Int(spec.Config, "max_steps"); ok {
				target.MaxSteps = value
			}
			if value, ok := config.Int(spec.Config, "max_replans"); ok {
				target.MaxReplans = value
			}
			if value, ok := config.Int(spec.Config, "max_tokens"); ok {
				target.MaxTokens = value
			}
			if value, ok := config.Float(spec.Config, "temperature"); ok {
				target.Temperature = value
			}
			target.Thinking = llms.ThinkingMode(config.String(spec.Config, "thinking"))
			if !validPlanThinkingMode(target.Thinking) {
				return nil, fmt.Errorf("build plan generator node %q: invalid thinking mode %q", spec.ID, target.Thinking)
			}
			if target.MaxSteps <= 0 {
				return nil, fmt.Errorf("build plan generator node %q: max_steps must be greater than 0", spec.ID)
			}
			if target.MaxReplans < 0 {
				return nil, fmt.Errorf("build plan generator node %q: max_replans must not be negative", spec.ID)
			}
			if target.MaxTokens < 0 || target.Temperature < 0 || target.Temperature > 2 {
				return nil, fmt.Errorf("build plan generator node %q: invalid model budget or temperature", spec.ID)
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
		"objective":                     objective,
		"is_replan":                     isReplan,
		"replan_reason":                 stringValue(current[planFieldReplanReason]),
		"previous_summary":              stringValue(current[planFieldSummary]),
		"previous_steps":                stepMaps(previousSteps),
		"available_tools":               toolDescriptions(availableTools),
		"configured_verifier":           strings.TrimSpace(n.VerifierID),
		"default_verification_strategy": strings.TrimSpace(n.DefaultVerificationStrategy),
	}
	prompt, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("plan generator node: encode prompt: %w", err)
	}
	temperature := n.effectiveTemperature()
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		ModelID: effectiveModelID(n.ModelID),
		Mode:    llms.ModelModeChat,
		Messages: []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveSystemPrompt()),
			llms.TextParts(llms.ChatMessageTypeHuman, "Create the execution plan from this JSON payload:\n\n"+string(prompt)),
		},
		Thinking:       n.effectiveThinking(),
		Temperature:    &temperature,
		MaxTokens:      n.MaxTokens,
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
	knownTools := toolNames(availableTools)
	steps := normalizePlanSteps(parsed.Steps, n.effectiveMaxSteps(), knownTools)
	if len(steps) == 0 {
		return errors.New("plan generator node: model produced no valid steps")
	}
	if strategy := strings.TrimSpace(n.DefaultVerificationStrategy); strategy != "" {
		for index := range steps {
			steps[index].VerificationStrategy = strategy
		}
	} else if verifierID := strings.TrimSpace(n.VerifierID); verifierID != "" {
		steps[len(steps)-1].VerificationStrategy = verifierID
	}
	configuredStrategy := strings.TrimSpace(n.DefaultVerificationStrategy)
	if configuredStrategy == "" {
		configuredStrategy = strings.TrimSpace(n.VerifierID)
	}
	steps = enforcePlanInvariants(objective, configuredStrategy, steps, knownTools)

	history := mapSlice(current[planFieldHistory])
	if history == nil {
		history = []map[string]any{}
	}
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
		planFieldSummary:      canonicalPlanSummary(steps),
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

func (n *GeneratorNode) effectiveTemperature() float64 {
	if n == nil || n.Temperature <= 0 {
		return 0.2
	}
	return n.Temperature
}

func (n *GeneratorNode) effectiveThinking() llms.ThinkingMode {
	if n == nil || n.Thinking == "" {
		return llms.ThinkingModeNone
	}
	return n.Thinking
}
