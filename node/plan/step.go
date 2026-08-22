package plan

import (
	"fmt"
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

const defaultPlanStepMaxIterations = 4
const defaultPlanStepPromptMaxChars = 24000

const defaultPlanStepSystemPrompt = `You are the implementation worker for exactly one step of a larger engineering plan.
Use the available tools actively: inspect existing files before editing, make changes in the requested scope, and run the most focused tests or checks that validate your work.
For coding tasks, do not merely describe code in chat: create or update the files, compile or test them, and fix failures before reporting success.
Treat tool output as evidence. Never claim a test passed unless you actually ran it and saw a successful exit status.
When the step has enough evidence, stop calling tools and return a concise result for this step only, including changed paths and commands run.
Do not synthesize the overall final answer. Use the same language as the objective.`

type StepNode struct {
	core.NodeBase
	SystemPrompt     string
	MaxIterations    int
	PromptMaxChars   int
	PlanPath         state.Path
	ExecutionPath    state.Path
	ConversationPath state.Path
}

func NewStepNode(options ...core.NodeOption) *StepNode {
	target := &StepNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeTypePlanStep,
			Description: "Prepare the current plan step for an LLM/tool execution loop.",
		}),
		SystemPrompt:   defaultPlanStepSystemPrompt,
		MaxIterations:  defaultPlanStepMaxIterations,
		PromptMaxChars: defaultPlanStepPromptMaxChars,
	}
	applyNodeOptions(&target.NodeBase, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *StepNode) Validate() error {
	if n == nil {
		return fmt.Errorf("plan step node is nil")
	}
	if err := n.NodeBase.Validate(); err != nil {
		return err
	}
	if n.PlanPath.Empty() || n.ExecutionPath.Empty() || n.ConversationPath.Empty() {
		return fmt.Errorf("plan step node %q requires plan, execution, and conversation paths", n.ID())
	}
	if n.MaxIterations <= 0 || n.PromptMaxChars <= 0 {
		return fmt.Errorf("plan step node %q has invalid iteration or prompt budget", n.ID())
	}
	return nil
}

func (n *StepNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.NodeBase, NodeTypePlanStep, map[string]any{
		"system_prompt":    n.SystemPrompt,
		"max_iterations":   n.MaxIterations,
		"prompt_max_chars": n.PromptMaxChars,
	}, map[string]state.Path{"plan": n.PlanPath, "execution": n.ExecutionPath, "conversation": n.ConversationPath})
}

func StepNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypePlanStep,
			Title:       "Plan Step",
			Description: "Prepare the current plan step for an LLM/tool execution loop.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"system_prompt": dsl.JSONSchema{
						"type":      "string",
						"title":     "System Prompt",
						"x-control": "textarea",
					},
					"max_iterations": dsl.JSONSchema{
						"type": "integer", "title": "Max Agent Iterations", "minimum": 1,
						"description": "Maximum model and tool loop iterations allowed for the current plan step.",
					},
					"prompt_max_chars": dsl.JSONSchema{
						"type": "integer", "title": "Step Prompt Max Characters", "minimum": 1,
						"description": "Maximum prompt size sent to the worker model for one plan step.",
					},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("plan", "Plan step selection and status.", plancap.CapabilityID, true,
				capabilityField(plancap.FieldObjective, dsl.StateAccessRead),
				capabilityField(plancap.FieldStatus, dsl.StateAccessWrite),
				capabilityField(plancap.FieldSummary, dsl.StateAccessRead),
				capabilityField(plancap.FieldSteps, dsl.StateAccessReadWrite),
				capabilityField(plancap.FieldCurrentIndex, dsl.StateAccessRead)),
			capabilityPort("execution", "Current step execution state.", executioncap.CapabilityID, true,
				capabilityField(executioncap.FieldCurrentStep, dsl.StateAccessWrite)),
			capabilityPort("conversation", "Conversation seeded for the selected step.", conversationcap.CapabilityID, true,
				capabilityField(conversationcap.FieldMessages, dsl.StateAccessWrite),
				capabilityField(conversationcap.FieldFinalAnswer, dsl.StateAccessWrite),
				capabilityField(conversationcap.FieldIterationCount, dsl.StateAccessWrite),
				capabilityField(conversationcap.FieldMaxIterations, dsl.StateAccessWrite)),
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
			target := NewStepNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			if _, exists := spec.Config["system_prompt"]; exists {
				target.SystemPrompt = config.String(spec.Config, "system_prompt")
			}
			if value, ok := config.Int(spec.Config, "max_iterations"); ok {
				target.MaxIterations = value
			}
			if value, ok := config.Int(spec.Config, "prompt_max_chars"); ok {
				target.PromptMaxChars = value
			}
			if target.MaxIterations <= 0 {
				return nil, fmt.Errorf("build plan step node %q: max_iterations must be greater than 0", spec.ID)
			}
			if target.PromptMaxChars <= 0 {
				return nil, fmt.Errorf("build plan step node %q: prompt_max_chars must be greater than 0", spec.ID)
			}
			target.PlanPath = planPath
			target.ExecutionPath = executionPath
			target.ConversationPath = conversationPath
			return target, nil
		},
	}
}

func (n *StepNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *StepNode) execute(_ core.Context, access *state.Access) error {
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
	steps := stepsFromValue(planValue[planFieldSteps])
	index := intValue(planValue[planFieldCurrentIndex])
	if index < 0 || index >= len(steps) {
		if err := planner.SetField(planFieldStatus, PlanStatusFinalizing); err != nil {
			return err
		}
		return execution.SetCurrentStep(map[string]any{})
	}

	step := steps[index]
	step.Status = PlanStepStatusRunning
	if step.StartedAt == "" {
		step.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	step.Error = ""
	steps[index] = step
	if err := planner.SetField(planFieldSteps, stepMaps(steps)); err != nil {
		return err
	}
	currentStep := stepMaps([]plancap.Step{step})[0]
	currentStep["index"] = index
	if err := execution.SetCurrentStep(currentStep); err != nil {
		return err
	}
	messages := make([]llms.MessageContent, 0, 2)
	if prompt := strings.TrimSpace(n.SystemPrompt); prompt != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, prompt))
	}
	stepPrompt := buildPlanStepPrompt(planValue, steps, index)
	if len(stepPrompt) > n.effectivePromptMaxChars() {
		stepPrompt = textLimit(stepPrompt, n.effectivePromptMaxChars())
	}
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, stepPrompt))
	if err := conversation.SetMessages(messages); err != nil {
		return err
	}
	if err := conversation.SetFinalAnswer(""); err != nil {
		return err
	}
	if err := conversation.ResetIteration(); err != nil {
		return err
	}
	return conversation.SetMaxIterations(n.effectiveMaxIterations())
}

func (n *StepNode) effectiveMaxIterations() int {
	if n == nil || n.MaxIterations <= 0 {
		return defaultPlanStepMaxIterations
	}
	return n.MaxIterations
}

func (n *StepNode) effectivePromptMaxChars() int {
	if n == nil || n.PromptMaxChars <= 0 {
		return defaultPlanStepPromptMaxChars
	}
	return n.PromptMaxChars
}

func buildPlanStepPrompt(planValue map[string]any, steps []plancap.Step, index int) string {
	var builder strings.Builder
	builder.WriteString("Objective:\n")
	builder.WriteString(stringValue(planValue[planFieldObjective]))
	if summary := stringValue(planValue[planFieldSummary]); summary != "" {
		builder.WriteString("\n\nPlan summary:\n")
		builder.WriteString(summary)
	}
	if index > 0 {
		builder.WriteString("\n\nCompleted step results:\n")
		for _, previous := range steps[:index] {
			fmt.Fprintf(&builder, "- [%s] %s\n", previous.ID, previous.Title)
			if previous.Result != "" {
				fmt.Fprintf(&builder, "  result: %s\n", textLimit(previous.Result, 4000))
			}
			if previous.Error != "" {
				fmt.Fprintf(&builder, "  error: %s\n", textLimit(previous.Error, 1000))
			}
		}
	}
	current := steps[index]
	builder.WriteString("\nCurrent step:\n")
	fmt.Fprintf(&builder, "id: %s\ntitle: %s\ninstruction: %s\n", current.ID, current.Title, current.Description)
	if len(current.ToolIDs) > 0 {
		fmt.Fprintf(&builder, "suggested tools: %s\n", strings.Join(current.ToolIDs, ", "))
	}
	if len(current.Deliverables) > 0 {
		fmt.Fprintf(&builder, "deliverables:\n- %s\n", strings.Join(current.Deliverables, "\n- "))
	}
	if len(current.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&builder, "acceptance criteria:\n- %s\n", strings.Join(current.AcceptanceCriteria, "\n- "))
	}
	if current.VerificationSummary != "" {
		fmt.Fprintf(&builder, "previous verification feedback: %s\n", current.VerificationSummary)
	}
	builder.WriteString("\nReturn the concrete result for the current step.")
	return builder.String()
}
