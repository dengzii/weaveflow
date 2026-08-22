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
	executor "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

const NodeType = "agent"

const defaultPromptMaxChars = 800_000

var _ dsl.GraphNodeSpecProvider = (*Node)(nil)

// Config defines model, prompt, tool access, and execution limits shared by agent nodes and agent tools.
type Config struct {
	ModelID                string
	ToolIDs                []string
	SystemPrompt           string
	ReasoningEffort        string
	MaxIterations          int
	MaxToolCalls           int
	MaxTokens              int
	MaxOutputTokens        int
	MaxCost                float64
	PromptMaxChars         int
	Parallel               bool
	RequireToolFinalAnswer bool
}

type Node struct {
	core.NodeBase
	Config
	OutputSchema            state.JSONSchema
	ResponseName            string
	OutputJSON              bool
	OutputJSONCompatibility bool
	TaskPath                state.Path
	ConversationPath        state.Path
	ResultPath              state.Path
}

func NewNode(options ...core.NodeOption) *Node {
	target := &Node{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeType,
			Description: "Run a self-contained ReAct loop with configurable prompt and tools.",
		}),
		Config:                  Config{Parallel: true},
		OutputJSONCompatibility: true,
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
	if node.MaxIterations < 0 || node.MaxToolCalls < 0 || node.MaxTokens < 0 || node.MaxOutputTokens < 0 || node.MaxCost < 0 {
		return fmt.Errorf("agent node %q budget values cannot be negative", node.ID())
	}
	if !validReasoningEffort(node.effectiveReasoningEffort()) {
		return fmt.Errorf("agent node %q reasoning_effort is invalid", node.ID())
	}
	if node.RequireToolFinalAnswer && len(node.ToolIDs) == 0 {
		return fmt.Errorf("agent node %q require_tool_final_answer requires at least one tool_id", node.ID())
	}
	if err := state.ValidateJSONSchemaDefinition(node.OutputSchema); err != nil {
		return fmt.Errorf("agent node %q output schema: %w", node.ID(), err)
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
	if strings.TrimSpace(node.ReasoningEffort) != "" {
		configMap["reasoning_effort"] = strings.TrimSpace(node.ReasoningEffort)
	}
	if node.MaxIterations > 0 {
		configMap["max_iterations"] = node.MaxIterations
	}
	if node.MaxToolCalls > 0 {
		configMap["max_tool_calls"] = node.MaxToolCalls
	}
	if node.MaxTokens > 0 {
		configMap["max_tokens"] = node.MaxTokens
	}
	if node.MaxOutputTokens > 0 {
		configMap["max_output_tokens"] = node.MaxOutputTokens
	}
	if node.MaxCost > 0 {
		configMap["max_cost"] = node.MaxCost
	}
	if node.PromptMaxChars > 0 {
		configMap["prompt_max_chars"] = node.PromptMaxChars
	}
	if len(node.OutputSchema) > 0 {
		configMap["output_schema"] = node.OutputSchema.Clone()
	}
	if strings.TrimSpace(node.ResponseName) != "" {
		configMap["response_name"] = node.ResponseName
	}
	configMap["output_json"] = node.OutputJSON
	configMap["output_json_compatibility"] = node.OutputJSONCompatibility
	configMap["require_tool_final_answer"] = node.RequireToolFinalAnswer
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
					"model_id": dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"tool_ids": dsl.JSONSchema{"type": "array", "title": "Tools", "items": dsl.JSONSchema{"type": "string"}},
					"system_prompt": dsl.JSONSchema{
						"type":      "string",
						"title":     "System Prompt",
						"x-control": "textarea",
					},
					"reasoning_effort": dsl.JSONSchema{
						"type": "string", "title": "Reasoning Effort",
						"enum":    []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"},
						"default": "high", "description": "Controls model reasoning effort when supported.",
					},
					"max_iterations": dsl.JSONSchema{
						"type": "integer", "title": "Max Agent Iterations", "minimum": 1, "default": 10,
						"description": "Maximum model and tool loop iterations before the agent stops.",
					},
					"max_tool_calls": dsl.JSONSchema{
						"type": "integer", "title": "Max Tool Calls", "minimum": 1,
						"description": "Maximum accepted tool calls across this Agent Run, including resumed execution.",
					},
					"max_tokens": dsl.JSONSchema{
						"type": "integer", "title": "Max Model Tokens", "minimum": 1,
						"description": "Maximum model tokens across this Agent Run, including resumed execution.",
					},
					"max_output_tokens": dsl.JSONSchema{
						"type": "integer", "title": "Max Output Tokens", "minimum": 1,
						"description": "Maximum tokens requested from the model for each response.",
					},
					"max_cost": dsl.JSONSchema{
						"type": "number", "title": "Max Model Cost", "minimum": 0,
						"description": "Maximum reported model cost across this Agent Run.",
					},
					"prompt_max_chars": dsl.JSONSchema{
						"type": "integer", "title": "Prompt Character Limit", "minimum": 1,
						"description": "Maximum character budget for conversation messages sent to the model; older messages are trimmed when exceeded.",
					},
					"parallel": dsl.JSONSchema{
						"type": "boolean", "title": "Parallel Tool Calls",
						"description": "Execute multiple tool calls from the same model response concurrently.",
					},
					"require_tool_final_answer": dsl.JSONSchema{
						"type": "boolean", "title": "Require Tool Final Answer", "default": false,
						"description": "Reject ordinary assistant text as terminal output and continue until a trusted tool supplies the final answer.",
					},
					"output_schema": dsl.JSONSchema{
						"type": "object", "title": "Output Schema",
						"description": "JSON Schema used to request and validate the final response. Setting it also enables JSON output.",
					},
					"response_name": dsl.JSONSchema{
						"type": "string", "title": "Response Name",
						"description": "Provider-facing name for the requested response format.",
					},
					"output_json": dsl.JSONSchema{
						"type": "boolean", "title": "Require JSON Output",
						"description": "Require the final answer to be a single JSON value even when no output schema is set.",
					},
					"output_json_compatibility": dsl.JSONSchema{
						"type": "boolean", "title": "JSON Compatibility Mode",
						"description": "Accept JSON embedded in Markdown fences or surrounding text instead of requiring the entire response to be one JSON value.",
					},
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
			{
				Name:          "result",
				Description:   "Final answer produced by the agent.",
				DefaultPath:   "shared.final.answer",
				Required:      true,
				Schema:        dsl.JSONSchema{"type": []string{"string", "object", "array", "number", "integer", "boolean", "null"}},
				Mode:          dsl.StateAccessWrite,
				MergeStrategy: dsl.StateMergeReplace,
			},
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
			target.ReasoningEffort = config.String(spec.Config, "reasoning_effort")
			target.TaskPath = taskPath
			target.ConversationPath = conversationPath
			target.ResultPath = resultPath
			target.MaxIterations, _ = config.Int(spec.Config, "max_iterations")
			target.MaxToolCalls, _ = config.Int(spec.Config, "max_tool_calls")
			target.MaxTokens, _ = config.Int(spec.Config, "max_tokens")
			target.MaxOutputTokens, _ = config.Int(spec.Config, "max_output_tokens")
			target.MaxCost, _ = config.Float(spec.Config, "max_cost")
			target.PromptMaxChars, _ = config.Int(spec.Config, "prompt_max_chars")
			target.OutputSchema, err = outputSchemaFromConfig(spec.Config)
			if err != nil {
				return nil, fmt.Errorf("agent node %q output schema: %w", spec.ID, err)
			}
			target.ResponseName = config.String(spec.Config, "response_name")
			if outputJSON, ok := config.Bool(spec.Config, "output_json"); ok {
				target.OutputJSON = outputJSON
			}
			if compatibility, ok := config.Bool(spec.Config, "output_json_compatibility"); ok {
				target.OutputJSONCompatibility = compatibility
			}
			if parallel, ok := config.Bool(spec.Config, "parallel"); ok {
				target.Parallel = parallel
			}
			if requireToolFinalAnswer, ok := config.Bool(spec.Config, "require_tool_final_answer"); ok {
				target.RequireToolFinalAnswer = requireToolFinalAnswer
			}
			if err := target.Validate(); err != nil {
				return nil, err
			}
			return target, nil
		},
	}
}

func (node *Node) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, node.execute(ctx, access)
}

func (node *Node) execute(ctx core.Context, access *state.Access) error {
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
	ctx = core.NewContext(executor.WithAgentStateProvider(ctx, func() *state.State {
		return access.State()
	}))
	runtime := newNodeRuntime(node)
	runtime.resumePhase = executor.AgentResumePhaseFromContext(ctx)
	if err := runtime.runLoop(ctx, conversation); err != nil {
		return err
	}
	outputJSON := node.outputJSONEnabled()
	answer, value, err := normalizeFinalOutput(conversation.FinalAnswer(), node.OutputSchema, outputJSON, node.OutputJSONCompatibility)
	if err != nil {
		return err
	}
	if !outputJSON {
		return state.Replace(access, state.NewRef[string](node.ResultPath), answer)
	}
	return state.Replace(access, state.NewRef[any](node.ResultPath), value)
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
	resultType := "string"
	resultSchema := state.JSONSchema{"type": "string"}
	if node.OutputJSON && len(node.OutputSchema) == 0 {
		resultType = ""
		resultSchema = state.JSONSchema{"type": []string{"string", "object", "array", "number", "integer", "boolean", "null"}}
	} else if len(node.OutputSchema) > 0 {
		resultType = node.OutputSchema.Type()
		resultSchema = node.OutputSchema.Clone()
	}
	return state.NewContract(
		state.FieldAccess{Path: node.TaskPath, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace, Type: "string"},
		state.FieldAccess{Path: node.ConversationPath.MustChild(conversationcap.FieldMessages), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "array"},
		state.FieldAccess{Path: node.ConversationPath.MustChild(conversationcap.FieldFinalAnswer), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "string"},
		state.FieldAccess{Path: node.ConversationPath.MustChild(conversationcap.FieldIterationCount), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "integer"},
		state.FieldAccess{Path: node.ConversationPath.MustChild(conversationcap.FieldMaxIterations), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "integer"},
		state.FieldAccess{Path: node.ResultPath, Mode: state.AccessWrite, Merge: state.MergeReplace, Type: resultType, Schema: resultSchema},
	)
}

func (node *Node) outputJSONEnabled() bool {
	return node != nil && (node.OutputJSON || len(node.OutputSchema) > 0)
}

func (node *Node) effectiveReasoningEffort() llms.ThinkingMode {
	if node == nil || strings.TrimSpace(node.ReasoningEffort) == "" {
		return llms.ThinkingModeHigh
	}
	return llms.ThinkingMode(strings.TrimSpace(node.ReasoningEffort))
}

func validReasoningEffort(value llms.ThinkingMode) bool {
	switch value {
	case llms.ThinkingModeAuto, llms.ThinkingModeNone, llms.ThinkingModeMinimal, llms.ThinkingModeLow,
		llms.ThinkingModeMedium, llms.ThinkingModeHigh, llms.ThinkingModeXHigh, llms.ThinkingModeMax:
		return true
	default:
		return false
	}
}

func outputSchemaFromConfig(values map[string]any) (state.JSONSchema, error) {
	if len(values) == 0 {
		return nil, nil
	}
	raw, exists := values["output_schema"]
	if !exists {
		return nil, nil
	}
	switch schema := raw.(type) {
	case state.JSONSchema:
		return schema.Clone(), nil
	case dsl.JSONSchema:
		return state.JSONSchema(schema.Clone()), nil
	case map[string]any:
		return state.JSONSchema(schema).Clone(), nil
	default:
		return nil, fmt.Errorf("must be an object, got %T", raw)
	}
}

type toolArtifact struct {
	Type     string                   `json:"type,omitempty"`
	Function *llms.FunctionDefinition `json:"function,omitempty"`
}

type promptArtifact struct {
	AgentNodeID             string                    `json:"agent_node_id,omitempty"`
	AgentToolName           string                    `json:"agent_tool_name,omitempty"`
	ConversationPath        string                    `json:"conversation_path,omitempty"`
	Iteration               int                       `json:"agent_iteration"`
	MaxIterations           int                       `json:"max_iterations,omitempty"`
	Messages                []conversationcap.Message `json:"messages,omitempty"`
	Tools                   []toolArtifact            `json:"tools,omitempty"`
	ResponseName            string                    `json:"response_name,omitempty"`
	OutputSchema            state.JSONSchema          `json:"output_schema,omitempty"`
	OutputJSON              bool                      `json:"output_json"`
	OutputJSONCompatibility bool                      `json:"output_json_compatibility"`
}

type responseArtifact struct {
	AgentNodeID             string                               `json:"agent_node_id,omitempty"`
	AgentToolName           string                               `json:"agent_tool_name,omitempty"`
	Iteration               int                                  `json:"agent_iteration"`
	Choices                 []basenode.LLMResponseArtifactChoice `json:"choices,omitempty"`
	ResponseName            string                               `json:"response_name,omitempty"`
	OutputSchema            state.JSONSchema                     `json:"output_schema,omitempty"`
	OutputJSON              bool                                 `json:"output_json"`
	OutputJSONCompatibility bool                                 `json:"output_json_compatibility"`
}

func buildPromptArtifact(identity executionIdentity, messages []llms.MessageContent, tools []llms.ToolDefinition, iteration, maxIterations int, responseName string, outputSchema state.JSONSchema, outputJSON, outputJSONCompatibility bool) (promptArtifact, error) {
	serialized, err := conversationcap.SerializeMessages(messages)
	if err != nil {
		return promptArtifact{}, err
	}
	payload := promptArtifact{
		AgentNodeID:             identity.NodeID,
		AgentToolName:           identity.ToolName,
		ConversationPath:        identity.ConversationPath,
		Iteration:               iteration,
		MaxIterations:           maxIterations,
		Messages:                serialized,
		ResponseName:            strings.TrimSpace(responseName),
		OutputSchema:            outputSchema.Clone(),
		OutputJSON:              outputJSON,
		OutputJSONCompatibility: outputJSONCompatibility,
	}
	if len(tools) > 0 {
		payload.Tools = make([]toolArtifact, 0, len(tools))
		for _, tool := range tools {
			payload.Tools = append(payload.Tools, toolArtifact{Type: tool.Type, Function: tool.Function})
		}
	}
	return payload, nil
}

func buildResponseArtifact(identity executionIdentity, response *llms.ModelResponse, iteration int, responseName string, outputSchema state.JSONSchema, outputJSON, outputJSONCompatibility bool) responseArtifact {
	if response == nil {
		return responseArtifact{}
	}
	inner := basenode.BuildLLMResponseArtifact(response)
	return responseArtifact{
		AgentNodeID:             identity.NodeID,
		AgentToolName:           identity.ToolName,
		Iteration:               iteration,
		Choices:                 inner.Choices,
		ResponseName:            strings.TrimSpace(responseName),
		OutputSchema:            outputSchema.Clone(),
		OutputJSON:              outputJSON,
		OutputJSONCompatibility: outputJSONCompatibility,
	}
}
