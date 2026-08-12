package agent

import (
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

const NodeType = "agent"

const defaultPromptMaxChars = 1000000

var _ dsl.GraphNodeSpecProvider = (*Node)(nil)

// Config defines model, prompt, tool access, and execution limits shared by agent nodes and agent tools.
type Config struct {
	ModelID        string
	ToolIDs        []string
	SystemPrompt   string
	MaxIterations  int
	PromptMaxChars int
	Parallel       bool
}

type Node struct {
	core.NodeBase
	Config
	TaskPath         state.Path
	ConversationPath state.Path
	ResultPath       state.Path
}

func NewNode(options ...core.NodeOption) *Node {
	target := &Node{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeType,
			Description: "Run a self-contained ReAct loop with configurable prompt and tools.",
		}),
		Config: Config{Parallel: true},
	}
	core.ApplyNodeOptions(&target.NodeBase, options)
	target.ApplyDefaultStatePaths()
	return target
}

func (node *Node) Validate() error {
	if node == nil {
		return fmt.Errorf("agent node is nil")
	}
	if err := node.NodeBase.Validate(); err != nil {
		return err
	}
	if node.TaskPath.Empty() || node.ConversationPath.Empty() || node.ResultPath.Empty() {
		return fmt.Errorf("agent node %q requires task, conversation, and result paths", node.ID())
	}
	return nil
}

func (node *Node) ApplyDefaultStatePaths() {
	if node == nil {
		return
	}
	owner := strings.TrimSpace(node.ID())
	if owner == "" {
		return
	}
	owner = strings.ReplaceAll(owner, ".", "_")
	if node.TaskPath.Empty() {
		node.TaskPath = state.Shared("request", "input")
	}
	if node.ConversationPath.Empty() {
		node.ConversationPath = state.Scope(owner, "conversation")
	}
	if node.ResultPath.Empty() {
		node.ResultPath = state.Shared("final", "answer")
	}
}

func (node *Node) GraphNodeSpec() dsl.GraphNodeSpec {
	if node == nil {
		return dsl.GraphNodeSpec{}
	}
	configMap := map[string]any{
		"tool_ids":      node.ToolIDs,
		"system_prompt": node.SystemPrompt,
		"parallel":      node.Parallel,
	}
	if strings.TrimSpace(node.ModelID) != "" {
		configMap["model_id"] = node.ModelID
	}
	if node.MaxIterations > 0 {
		configMap["max_iterations"] = node.MaxIterations
	}
	if node.PromptMaxChars > 0 {
		configMap["prompt_max_chars"] = node.PromptMaxChars
	}
	return basenode.NewGraphNodeSpec(node.NodeBase, NodeType, configMap, map[string]state.Path{
		"task": node.TaskPath, "conversation": node.ConversationPath, "result": node.ResultPath,
	})
}

func NodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeType,
			Title:       "Agent",
			Description: "Run a self-contained ReAct loop: LLM inference and tool execution iterate inside the node until a final answer or the iteration cap is reached.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string"},
					"tool_ids": dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"system_prompt": dsl.JSONSchema{
						"type":      "string",
						"title":     "System Prompt",
						"x-control": "textarea",
					},
					"max_iterations":   dsl.JSONSchema{"type": "integer", "minimum": 1},
					"prompt_max_chars": dsl.JSONSchema{"type": "integer", "minimum": 1},
					"parallel":         dsl.JSONSchema{"type": "boolean"},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			basenode.PrimitivePort("task", "Initial task for the agent.", "string", dsl.StateAccessRead, true),
			basenode.CapabilityPort("conversation", "Conversation owned by this agent loop.", conversationcap.CapabilityID, true,
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldFinalAnswer, Mode: dsl.StateAccessReadWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldIterationCount, Mode: dsl.StateAccessReadWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldMaxIterations, Mode: dsl.StateAccessReadWrite},
			),
			basenode.PrimitivePort("result", "Final answer produced by the agent.", "string", dsl.StateAccessWrite, true),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			taskPath, err := basenode.ResolvedPath(resolved, "task")
			if err != nil {
				return nil, err
			}
			conversationPath, err := basenode.ResolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			resultPath, err := basenode.ResolvedPath(resolved, "result")
			if err != nil {
				return nil, err
			}
			target := NewNode(core.WithID(spec.ID))
			basenode.ApplyNodeMetadata(&target.NodeBase, spec)
			target.ModelID = config.String(spec.Config, "model_id")
			target.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			target.SystemPrompt = config.String(spec.Config, "system_prompt")
			target.TaskPath = taskPath
			target.ConversationPath = conversationPath
			target.ResultPath = resultPath
			target.MaxIterations, _ = config.Int(spec.Config, "max_iterations")
			target.PromptMaxChars, _ = config.Int(spec.Config, "prompt_max_chars")
			if parallel, ok := config.Bool(spec.Config, "parallel"); ok {
				target.Parallel = parallel
			}
			return target, nil
		},
	}
}

func (node *Node) Execute(ctx core.Context, access *state.Access) error {
	if ctx.Model(node.ModelID) == nil {
		return fmt.Errorf("agent node: model %q not available", effectiveModelID(node.ModelID))
	}

	conversation, err := conversationcap.Bind(access, node.ConversationPath)
	if err != nil {
		return err
	}
	if node.MaxIterations > 0 && conversation.MaxIterations() < node.MaxIterations {
		if err := conversation.SetMaxIterations(node.MaxIterations); err != nil {
			return err
		}
	}

	task, err := state.Get(access, state.NewRef[string](node.TaskPath))
	if err != nil {
		return err
	}
	if err := node.SeedConversation(conversation, strings.TrimSpace(task)); err != nil {
		return err
	}
	if err := node.RunLoop(ctx, conversation); err != nil {
		return err
	}
	return state.Replace(access, state.NewRef[string](node.ResultPath), strings.TrimSpace(conversation.FinalAnswer()))
}

func (node *Node) SeedConversation(conversation *conversationcap.View, task string) error {
	return newNodeRuntime(node).seedConversation(conversation, task)
}

func (node *Node) RunLoop(ctx core.Context, conversation *conversationcap.View) error {
	return newNodeRuntime(node).runLoop(ctx, conversation)
}

func (node *Node) Contract() state.Contract {
	if node == nil {
		return state.Contract{}
	}
	return state.NewContract(
		state.FieldAccess{Path: node.TaskPath, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace, Type: "string"},
		state.FieldAccess{Path: node.ConversationPath.MustChild(conversationcap.FieldMessages), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "array"},
		state.FieldAccess{Path: node.ConversationPath.MustChild(conversationcap.FieldFinalAnswer), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "string"},
		state.FieldAccess{Path: node.ConversationPath.MustChild(conversationcap.FieldIterationCount), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "integer"},
		state.FieldAccess{Path: node.ConversationPath.MustChild(conversationcap.FieldMaxIterations), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "integer"},
		state.FieldAccess{Path: node.ResultPath, Mode: state.AccessWrite, Merge: state.MergeReplace, Type: "string"},
	)
}

type toolArtifact struct {
	Type     string                   `json:"type,omitempty"`
	Function *llms.FunctionDefinition `json:"function,omitempty"`
}

type promptArtifact struct {
	AgentNodeID      string                    `json:"agent_node_id,omitempty"`
	AgentToolName    string                    `json:"agent_tool_name,omitempty"`
	ConversationPath string                    `json:"conversation_path,omitempty"`
	Iteration        int                       `json:"agent_iteration"`
	MaxIterations    int                       `json:"max_iterations,omitempty"`
	Messages         []conversationcap.Message `json:"messages,omitempty"`
	Tools            []toolArtifact            `json:"tools,omitempty"`
}

type responseArtifact struct {
	AgentNodeID   string                               `json:"agent_node_id,omitempty"`
	AgentToolName string                               `json:"agent_tool_name,omitempty"`
	Iteration     int                                  `json:"agent_iteration"`
	Choices       []basenode.LLMResponseArtifactChoice `json:"choices,omitempty"`
}

func buildPromptArtifact(identity executionIdentity, messages []llms.MessageContent, tools []llms.Tool, iteration, maxIterations int) (promptArtifact, error) {
	serialized, err := conversationcap.SerializeMessages(messages)
	if err != nil {
		return promptArtifact{}, err
	}
	payload := promptArtifact{
		AgentNodeID:      identity.NodeID,
		AgentToolName:    identity.ToolName,
		ConversationPath: identity.ConversationPath,
		Iteration:        iteration,
		MaxIterations:    maxIterations,
		Messages:         serialized,
	}
	if len(tools) > 0 {
		payload.Tools = make([]toolArtifact, 0, len(tools))
		for _, tool := range tools {
			payload.Tools = append(payload.Tools, toolArtifact{Type: tool.Type, Function: tool.Function})
		}
	}
	return payload, nil
}

func buildResponseArtifact(identity executionIdentity, response *llms.ContentResponse, iteration int) responseArtifact {
	if response == nil {
		return responseArtifact{}
	}
	inner := basenode.BuildLLMResponseArtifact(response)
	return responseArtifact{
		AgentNodeID:   identity.NodeID,
		AgentToolName: identity.ToolName,
		Iteration:     iteration,
		Choices:       inner.Choices,
	}
}
