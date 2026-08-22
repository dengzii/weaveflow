package supervisor

import (
	"encoding/json"
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/llms"
	agentnode "github.com/dengzii/weaveflow/node/agents/agent"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

const defaultSupervisorWorkerMaxIterations = 6

const defaultSupervisorWorkerSystemPrompt = `You are a specialist worker in a supervised team.
Complete only the delegated task. Use your tools when they improve accuracy.
Return a concise, evidence-based result to the supervisor; do not address the end user or delegate to another worker.`

type WorkerNode struct {
	core.NodeBase
	WorkerID               string
	Role                   string
	ModelID                string
	ToolIDs                []string
	SystemPrompt           string
	ReasoningEffort        string
	MaxIterations          int
	MaxOutputTokens        int
	PromptMaxChars         int
	HistoryTurns           int
	Parallel               bool
	RequireToolFinalAnswer bool
	SupervisorPath         state.Path
	ConversationPath       state.Path
}

func NewWorkerNode(options ...core.NodeOption) *WorkerNode {
	target := &WorkerNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeTypeSupervisorWorker,
			Description: "Execute one supervisor delegation with an isolated specialist agent loop.",
		}),
		SystemPrompt:  defaultSupervisorWorkerSystemPrompt,
		MaxIterations: defaultSupervisorWorkerMaxIterations,
		Parallel:      true,
	}
	applyNodeOptions(&target.NodeBase, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *WorkerNode) Validate() error {
	if n == nil {
		return fmt.Errorf("supervisor worker node is nil")
	}
	if err := n.NodeBase.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(n.WorkerID) == "" {
		return fmt.Errorf("supervisor worker node %q requires worker_id", n.ID())
	}
	if strings.EqualFold(strings.TrimSpace(n.WorkerID), SupervisorRouteFinish) {
		return fmt.Errorf("supervisor worker node %q cannot use reserved worker_id %q", n.ID(), SupervisorRouteFinish)
	}
	if n.effectiveMaxIterations() < 1 {
		return fmt.Errorf("supervisor worker node %q max_iterations must be positive", n.ID())
	}
	if n.MaxOutputTokens < 0 {
		return fmt.Errorf("supervisor worker node %q max_output_tokens cannot be negative", n.ID())
	}
	if !validWorkerReasoningEffort(n.effectiveReasoningEffort()) {
		return fmt.Errorf("supervisor worker node %q reasoning_effort is invalid", n.ID())
	}
	if n.HistoryTurns < 0 {
		return fmt.Errorf("supervisor worker node %q history_turns cannot be negative", n.ID())
	}
	if n.RequireToolFinalAnswer && len(n.ToolIDs) == 0 {
		return fmt.Errorf("supervisor worker node %q require_tool_final_answer requires at least one tool_id", n.ID())
	}
	if n.SupervisorPath.Empty() || n.ConversationPath.Empty() {
		return fmt.Errorf("supervisor worker node %q requires supervisor and conversation paths", n.ID())
	}
	return nil
}

func (n *WorkerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	configMap := map[string]any{
		"worker_id":                 n.WorkerID,
		"role":                      n.Role,
		"model_id":                  n.ModelID,
		"tool_ids":                  n.ToolIDs,
		"system_prompt":             n.SystemPrompt,
		"max_iterations":            n.effectiveMaxIterations(),
		"parallel":                  n.Parallel,
		"require_tool_final_answer": n.RequireToolFinalAnswer,
	}
	if n.PromptMaxChars > 0 {
		configMap["prompt_max_chars"] = n.PromptMaxChars
	}
	if n.MaxOutputTokens > 0 {
		configMap["max_output_tokens"] = n.MaxOutputTokens
	}
	if strings.TrimSpace(n.ReasoningEffort) != "" {
		configMap["reasoning_effort"] = strings.TrimSpace(n.ReasoningEffort)
	}
	if n.HistoryTurns > 0 {
		configMap["history_turns"] = n.HistoryTurns
	}
	return newGraphNodeSpec(n.NodeBase, NodeTypeSupervisorWorker, configMap, map[string]state.Path{
		"supervisor": n.SupervisorPath, "conversation": n.ConversationPath,
	})
}

func WorkerNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeSupervisorWorker,
			Title:       "Supervisor Worker",
			Description: "Run one specialist ReAct loop for the task selected by a Supervisor node, then append the result to shared supervisor history.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"worker_id": dsl.JSONSchema{"type": "string", "title": "Worker ID", "description": "Must match the member id configured on the Supervisor node."},
					"role":      dsl.JSONSchema{"type": "string", "title": "Role", "description": "Short capability description added to this worker's system prompt."},
					"model_id":  dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"tool_ids":  dsl.JSONSchema{"type": "array", "title": "Tools", "items": dsl.JSONSchema{"type": "string"}},
					"system_prompt": dsl.JSONSchema{
						"type": "string", "title": "System Prompt", "x-control": "textarea", "default": defaultSupervisorWorkerSystemPrompt,
					},
					"max_iterations": dsl.JSONSchema{"type": "integer", "title": "Max Agent Iterations", "minimum": 1, "default": defaultSupervisorWorkerMaxIterations},
					"max_output_tokens": dsl.JSONSchema{
						"type": "integer", "title": "Max Output Tokens", "minimum": 1,
						"description": "Maximum tokens requested from the model for each worker response.",
					},
					"reasoning_effort": dsl.JSONSchema{
						"type": "string", "title": "Reasoning Effort",
						"enum":    []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"},
						"default": "high", "description": "Controls model reasoning effort when supported.",
					},
					"prompt_max_chars": dsl.JSONSchema{
						"type": "integer", "title": "Prompt Character Limit", "minimum": 1,
						"description": "Maximum character budget for conversation messages sent to the model; older messages are trimmed when exceeded.",
					},
					"history_turns": dsl.JSONSchema{
						"type": "integer", "title": "History Turns", "minimum": 0,
						"description": "Number of recent completed delegations included with the worker task. Zero keeps tasks isolated.",
					},
					"parallel": dsl.JSONSchema{
						"type": "boolean", "title": "Parallel Tool Calls", "default": true,
						"description": "Execute multiple tool calls from the same model response concurrently.",
					},
					"require_tool_final_answer": dsl.JSONSchema{
						"type": "boolean", "title": "Require Tool Final Answer", "default": false,
						"description": "Continue the worker loop until a trusted tool supplies its final answer.",
					},
				},
				"required":             []string{"worker_id"},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("supervisor", "Selected task and shared worker history.", supervisorcap.CapabilityID, true,
				capabilityField(supervisorcap.FieldObjective, dsl.StateAccessRead),
				capabilityField(supervisorcap.FieldRoute, dsl.StateAccessReadWrite),
				capabilityField(supervisorcap.FieldTask, dsl.StateAccessReadWrite),
				capabilityField(supervisorcap.FieldStatus, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldTurnCount, dsl.StateAccessRead),
				capabilityField(supervisorcap.FieldHistory, dsl.StateAccessReadWrite),
				capabilityField(supervisorcap.FieldLastResult, dsl.StateAccessWrite)),
			capabilityPort("conversation", "Isolated conversation for this worker.", conversationcap.CapabilityID, true,
				capabilityField(conversationcap.FieldMessages, dsl.StateAccessReadWrite),
				capabilityField(conversationcap.FieldFinalAnswer, dsl.StateAccessReadWrite),
				capabilityField(conversationcap.FieldIterationCount, dsl.StateAccessReadWrite),
				capabilityField(conversationcap.FieldMaxIterations, dsl.StateAccessReadWrite)),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			supervisorPath, err := resolvedPath(resolved, "supervisor")
			if err != nil {
				return nil, err
			}
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			target := NewWorkerNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			target.WorkerID = config.String(spec.Config, "worker_id")
			target.Role = config.String(spec.Config, "role")
			target.ModelID = config.String(spec.Config, "model_id")
			target.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			target.ReasoningEffort = config.String(spec.Config, "reasoning_effort")
			if prompt := config.String(spec.Config, "system_prompt"); strings.TrimSpace(prompt) != "" {
				target.SystemPrompt = prompt
			}
			if value, ok := config.Int(spec.Config, "max_iterations"); ok {
				target.MaxIterations = value
			}
			if value, ok := config.Int(spec.Config, "max_output_tokens"); ok {
				target.MaxOutputTokens = value
			}
			if value, ok := config.Int(spec.Config, "prompt_max_chars"); ok {
				target.PromptMaxChars = value
			}
			if value, ok := config.Int(spec.Config, "history_turns"); ok {
				target.HistoryTurns = value
			}
			if value, ok := config.Bool(spec.Config, "parallel"); ok {
				target.Parallel = value
			}
			if value, ok := config.Bool(spec.Config, "require_tool_final_answer"); ok {
				target.RequireToolFinalAnswer = value
			}
			target.SupervisorPath = supervisorPath
			target.ConversationPath = conversationPath
			if err := target.Validate(); err != nil {
				return nil, err
			}
			return target, nil
		},
	}
}

func (n *WorkerNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *WorkerNode) execute(ctx core.Context, access *state.Access) error {
	if err := n.Validate(); err != nil {
		return err
	}
	supervisorView, err := supervisorcap.Bind(access, n.SupervisorPath)
	if err != nil {
		return err
	}
	current := supervisorView.Value()
	selected := stringValue(current, SupervisorFieldRoute)
	if !strings.EqualFold(selected, strings.TrimSpace(n.WorkerID)) {
		return fmt.Errorf("supervisor worker node %q expected route %q, got %q", n.ID(), n.WorkerID, selected)
	}
	task := stringValue(current, SupervisorFieldTask)
	if task == "" {
		return fmt.Errorf("supervisor worker node %q received an empty task", n.ID())
	}

	conversation, err := conversationcap.Bind(access, n.ConversationPath)
	if err != nil {
		return err
	}
	workerAgent := agentnode.NewNode(core.WithID(n.ID() + "_agent"))
	workerAgent.ModelID = n.ModelID
	workerAgent.ToolIDs = append([]string(nil), n.ToolIDs...)
	workerAgent.SystemPrompt = n.effectiveSystemPrompt()
	workerAgent.ReasoningEffort = n.effectiveReasoningEffort()
	workerAgent.MaxIterations = n.effectiveMaxIterations()
	workerAgent.MaxOutputTokens = n.MaxOutputTokens
	workerAgent.PromptMaxChars = n.PromptMaxChars
	workerAgent.Parallel = n.Parallel
	workerAgent.RequireToolFinalAnswer = n.RequireToolFinalAnswer
	if err := conversation.SetMessages(nil); err != nil {
		return err
	}
	if err := conversation.SetFinalAnswer(""); err != nil {
		return err
	}
	if err := conversation.ResetIteration(); err != nil {
		return err
	}
	if err := conversation.SetMaxIterations(workerAgent.MaxIterations); err != nil {
		return err
	}
	workerTask := taskWithSupervisorContext(
		stringValue(current, SupervisorFieldObjective),
		task,
		historyFromValue(current[SupervisorFieldHistory]),
		n.HistoryTurns,
	)
	if err := workerAgent.SeedConversation(conversation, workerTask); err != nil {
		return err
	}
	if err := workerAgent.RunLoop(ctx, conversation); err != nil {
		return fmt.Errorf("supervisor worker %q: %w", n.WorkerID, err)
	}
	result := conversation.FinalAnswer()
	result = strings.TrimSpace(result)
	if result == "" {
		return fmt.Errorf("supervisor worker %q returned an empty result", n.WorkerID)
	}

	history := historyFromValue(current[SupervisorFieldHistory])
	turn := intValue(current, SupervisorFieldTurnCount)
	history = append(history, supervisorcap.Turn{Turn: turn, WorkerID: strings.TrimSpace(n.WorkerID), Task: task, Result: result})
	if err := supervisorView.Merge(map[string]any{
		SupervisorFieldHistory:    turnMaps(history),
		SupervisorFieldLastResult: result,
		SupervisorFieldStatus:     SupervisorStatusRouting,
		SupervisorFieldRoute:      "",
		SupervisorFieldTask:       "",
	}); err != nil {
		return err
	}
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "supervisor.worker.result", history[len(history)-1])
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"event": "supervisor.worker_completed", "worker_id": strings.TrimSpace(n.WorkerID), "turn_count": turn, "result": result,
	})
	return nil
}

func taskWithSupervisorContext(objective, task string, history []supervisorcap.Turn, historyTurns int) string {
	task = strings.TrimSpace(task)
	if historyTurns <= 0 {
		return task
	}
	if len(history) > historyTurns {
		history = history[len(history)-historyTurns:]
	}
	encodedHistory, _ := json.MarshalIndent(history, "", "  ")
	return fmt.Sprintf("Overall objective:\n%s\n\nRecent completed delegations:\n%s\n\nCurrent delegated task:\n%s",
		strings.TrimSpace(objective), encodedHistory, task)
}

func (n *WorkerNode) effectiveSystemPrompt() string {
	prompt := strings.TrimSpace(n.SystemPrompt)
	if prompt == "" {
		prompt = defaultSupervisorWorkerSystemPrompt
	}
	if role := strings.TrimSpace(n.Role); role != "" {
		prompt += "\n\nYour specialist role: " + role
	}
	return prompt
}

func (n *WorkerNode) effectiveMaxIterations() int {
	if n == nil || n.MaxIterations <= 0 {
		return defaultSupervisorWorkerMaxIterations
	}
	return n.MaxIterations
}

func (n *WorkerNode) effectiveReasoningEffort() string {
	if n == nil || strings.TrimSpace(n.ReasoningEffort) == "" {
		return string(llms.ThinkingModeHigh)
	}
	return strings.TrimSpace(n.ReasoningEffort)
}

func validWorkerReasoningEffort(value string) bool {
	switch llms.ThinkingMode(value) {
	case llms.ThinkingModeAuto, llms.ThinkingModeNone, llms.ThinkingModeMinimal, llms.ThinkingModeLow,
		llms.ThinkingModeMedium, llms.ThinkingModeHigh, llms.ThinkingModeXHigh, llms.ThinkingModeMax:
		return true
	default:
		return false
	}
}
