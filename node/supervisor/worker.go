package supervisor

import (
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

const defaultSupervisorWorkerMaxIterations = 6

const defaultSupervisorWorkerSystemPrompt = `You are a specialist worker in a supervised team.
Complete only the delegated task. Use your tools when they improve accuracy.
Return a concise, evidence-based result to the supervisor; do not address the end user or delegate to another worker.`

type SupervisorWorkerNode struct {
	core.NodeBase
	WorkerID         string
	Role             string
	ModelID          string
	ToolIDs          []string
	SystemPrompt     string
	MaxIterations    int
	PromptMaxChars   int
	Parallel         bool
	SupervisorPath   state.Path
	ConversationPath state.Path
}

func NewSupervisorWorkerNode(options ...core.NodeOption) *SupervisorWorkerNode {
	target := &SupervisorWorkerNode{
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

func (n *SupervisorWorkerNode) Validate() error {
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
	if n.SupervisorPath.Empty() || n.ConversationPath.Empty() {
		return fmt.Errorf("supervisor worker node %q requires supervisor and conversation paths", n.ID())
	}
	return nil
}

func (n *SupervisorWorkerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	configMap := map[string]any{
		"worker_id":      n.WorkerID,
		"role":           n.Role,
		"model_id":       n.ModelID,
		"tool_ids":       n.ToolIDs,
		"system_prompt":  n.SystemPrompt,
		"max_iterations": n.effectiveMaxIterations(),
		"parallel":       n.Parallel,
	}
	if n.PromptMaxChars > 0 {
		configMap["prompt_max_chars"] = n.PromptMaxChars
	}
	return newGraphNodeSpec(n.NodeBase, NodeTypeSupervisorWorker, configMap, map[string]state.Path{
		"supervisor": n.SupervisorPath, "conversation": n.ConversationPath,
	})
}

func SupervisorWorkerNodeTypeDefinition() registry.NodeTypeDefinition {
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
					"max_iterations":   dsl.JSONSchema{"type": "integer", "title": "Max Agent Iterations", "minimum": 1, "default": defaultSupervisorWorkerMaxIterations},
					"prompt_max_chars": dsl.JSONSchema{"type": "integer", "title": "Prompt Character Limit", "minimum": 1},
					"parallel":         dsl.JSONSchema{"type": "boolean", "title": "Parallel Tool Calls", "default": true},
				},
				"required":             []string{"worker_id"},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("supervisor", "Selected task and shared worker history.", supervisorcap.CapabilityID, true,
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
			target := NewSupervisorWorkerNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			target.WorkerID = config.String(spec.Config, "worker_id")
			target.Role = config.String(spec.Config, "role")
			target.ModelID = config.String(spec.Config, "model_id")
			target.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			if prompt := config.String(spec.Config, "system_prompt"); strings.TrimSpace(prompt) != "" {
				target.SystemPrompt = prompt
			}
			if value, ok := config.Int(spec.Config, "max_iterations"); ok {
				target.MaxIterations = value
			}
			if value, ok := config.Int(spec.Config, "prompt_max_chars"); ok {
				target.PromptMaxChars = value
			}
			if value, ok := config.Bool(spec.Config, "parallel"); ok {
				target.Parallel = value
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

func (n *SupervisorWorkerNode) Execute(ctx core.Context, access *state.Access) error {
	if err := n.Validate(); err != nil {
		return err
	}
	supervisor, err := supervisorcap.Bind(access, n.SupervisorPath)
	if err != nil {
		return err
	}
	current := supervisor.Value()
	selected := supervisorString(current, SupervisorFieldRoute)
	if !strings.EqualFold(selected, strings.TrimSpace(n.WorkerID)) {
		return fmt.Errorf("supervisor worker node %q expected route %q, got %q", n.ID(), n.WorkerID, selected)
	}
	task := supervisorString(current, SupervisorFieldTask)
	if task == "" {
		return fmt.Errorf("supervisor worker node %q received an empty task", n.ID())
	}

	conversation, err := conversationcap.Bind(access, n.ConversationPath)
	if err != nil {
		return err
	}
	agent := basenode.NewAgentNode(core.WithID(n.ID() + "_agent"))
	agent.ModelID = n.ModelID
	agent.ToolIDs = append([]string(nil), n.ToolIDs...)
	agent.SystemPrompt = n.effectiveSystemPrompt()
	agent.MaxIterations = n.effectiveMaxIterations()
	agent.PromptMaxChars = n.PromptMaxChars
	agent.Parallel = n.Parallel
	agent.ToolName = n.WorkerID
	agent.ToolDescription = n.Role
	if err := conversation.SetMessages(nil); err != nil {
		return err
	}
	if err := conversation.SetFinalAnswer(""); err != nil {
		return err
	}
	if err := conversation.ResetIteration(); err != nil {
		return err
	}
	if err := conversation.SetMaxIterations(agent.MaxIterations); err != nil {
		return err
	}
	if err := agent.SeedConversation(conversation, task); err != nil {
		return err
	}
	if err := agent.RunLoop(ctx, conversation); err != nil {
		return fmt.Errorf("supervisor worker %q: %w", n.WorkerID, err)
	}
	result := conversation.FinalAnswer()
	result = strings.TrimSpace(result)
	if result == "" {
		return fmt.Errorf("supervisor worker %q returned an empty result", n.WorkerID)
	}

	history := supervisorHistoryFromValue(current[SupervisorFieldHistory])
	turn := supervisorInt(current, SupervisorFieldTurnCount)
	history = append(history, supervisorcap.Turn{Turn: turn, WorkerID: strings.TrimSpace(n.WorkerID), Task: task, Result: result})
	if err := supervisor.Merge(map[string]any{
		SupervisorFieldHistory:    supervisorTurnMaps(history),
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

func (n *SupervisorWorkerNode) effectiveSystemPrompt() string {
	prompt := strings.TrimSpace(n.SystemPrompt)
	if prompt == "" {
		prompt = defaultSupervisorWorkerSystemPrompt
	}
	if role := strings.TrimSpace(n.Role); role != "" {
		prompt += "\n\nYour specialist role: " + role
	}
	return prompt
}

func (n *SupervisorWorkerNode) effectiveMaxIterations() int {
	if n == nil || n.MaxIterations <= 0 {
		return defaultSupervisorWorkerMaxIterations
	}
	return n.MaxIterations
}
