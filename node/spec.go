package node

import (
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node/stateops"
	"github.com/dengzii/weaveflow/state"
)

const (
	NodeTypeSubgraph            = "subgraph"
	NodeTypeUserInput           = "user_input"
	NodeTypeConversationMessage = "conversation_message"
	NodeTypeContextReducer      = "context_reducer"
	NodeTypeLLMTurn             = "llm_turn"
	NodeTypeTextGeneration      = "text_generation"
	NodeTypeToolExecution       = "tool_execution"
	NodeTypeEnvironmentContext  = "environment_context"
	NodeTypeExploreAgent        = "explore_agent"
	NodeTypeChatReply           = "chat_reply"
	NodeTypeStateSet            = stateops.NodeTypeStateSet
	NodeTypeStateCopy           = stateops.NodeTypeStateCopy
	NodeTypeStateDelete         = stateops.NodeTypeStateDelete
	NodeTypeStateMerge          = stateops.NodeTypeStateMerge
	NodeTypeStateAppend         = stateops.NodeTypeStateAppend
	NodeTypeStateTransform      = stateops.NodeTypeStateTransform
)

var (
	_ dsl.GraphNodeSpecProvider = (*SubgraphNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*UserInputNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ConversationMessageNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ContextReducerNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*LLMTurnNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*TextGenerationNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ToolExecutionNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*EnvironmentContextNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ExploreAgentNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ChatReplyNode)(nil)
)

type DefaultStatePathApplier interface {
	ApplyDefaultStatePaths()
}

func newGraphNodeSpec(base Base, nodeType string, config map[string]any, statePaths ...map[string]state.Path) dsl.GraphNodeSpec {
	return NewGraphNodeSpec(base, nodeType, config, statePaths...)
}

func NewGraphNodeSpec(base Base, nodeType string, config map[string]any, statePaths ...map[string]state.Path) dsl.GraphNodeSpec {
	spec := dsl.GraphNodeSpec{
		ID:          base.ID(),
		Name:        base.Name(),
		Type:        nodeType,
		Description: base.Description(),
		Config:      compactGraphNodeConfig(config),
		State:       graphStateBindings(base.ID(), nodeType, statePaths...),
	}
	if spec.Name == "" {
		spec.Name = spec.ID
	}
	return spec
}

func graphStateBindings(nodeID, nodeType string, statePaths ...map[string]state.Path) map[string]dsl.StateBinding {
	if len(statePaths) == 0 || len(statePaths[0]) == 0 {
		return nil
	}
	bindings := make(map[string]dsl.StateBinding, len(statePaths[0]))
	for name, path := range statePaths[0] {
		if path.Empty() {
			path = defaultNodeStatePath(nodeID, nodeType, name)
		}
		if path.Empty() {
			continue
		}
		bindings[name] = dsl.StateBinding{Path: path.String()}
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

func defaultNodeStatePath(nodeID, nodeType, port string) state.Path {
	template := defaultPrimitivePath(port)
	switch nodeType {
	case NodeTypeSubgraph:
		if port == "input" || port == "output" {
			template = "scopes.{node_id}." + port
		}
	case NodeTypeTextGeneration:
		switch port {
		case "prompt":
			template = "shared.text_generation.prompt"
		case "output":
			template = "shared.text_generation.result"
		}
	case NodeTypeUserInput:
		if port == "value" {
			template = "shared.request.input"
		}
	default:
		if port == "conversation" || port == "parent_conversation" {
			template = "scopes.{node_id}." + port
		}
	}
	template = strings.ReplaceAll(template, "{node_id}", defaultNodePathOwner(nodeID))
	path, err := state.ParsePath(template)
	if err != nil || len(path.Segments()) == 0 {
		return state.Path{}
	}
	return path
}

func defaultNodePathOwner(nodeID string) string {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		nodeID = "node"
	}
	return strings.ReplaceAll(nodeID, ".", "_")
}

func ApplyDefaultStatePaths(targetNode Node) {
	if targetNode == nil {
		return
	}
	if applier, ok := targetNode.(DefaultStatePathApplier); ok {
		applier.ApplyDefaultStatePaths()
		return
	}
	if strings.TrimSpace(targetNode.ID()) == "" {
		return
	}
	setShared := func(target *state.Path, segments ...string) {
		if target.Empty() {
			*target = state.Shared(segments...)
		}
	}
	setScope := func(target *state.Path, owner string, segments ...string) {
		if target.Empty() {
			*target = state.Scope(defaultNodePathOwner(owner), segments...)
		}
	}
	switch typed := targetNode.(type) {
	case *UserInputNode:
		setShared(&typed.ValuePath, "request", "input")
		setShared(&typed.PendingInputPath, "request", "pending_input")
	case *ConversationMessageNode:
		setShared(&typed.InputPath, "request", "input")
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeConversationMessage), "conversation")
	case *ContextReducerNode:
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeContextReducer), "conversation")
	case *LLMTurnNode:
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeLLMTurn), "conversation")
		setShared(&typed.OutputPath, "final", "answer")
	case *TextGenerationNode:
		setShared(&typed.PromptPath, "text_generation", "prompt")
		setShared(&typed.OutputPath, "text_generation", "result")
	case *ToolExecutionNode:
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeToolExecution), "conversation")
	case *ExploreAgentNode:
		setShared(&typed.TaskPath, "request", "input")
		setScope(&typed.ParentConversationPath, defaultNodeOwner(typed, NodeTypeExploreAgent), "parent_conversation")
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeExploreAgent), "conversation")
		setShared(&typed.EnvironmentPath, "environment")
		setShared(&typed.ResultPath, "final", "answer")
	case *EnvironmentContextNode:
		setShared(&typed.EnvironmentPath, "environment")
	case *ChatReplyNode:
		setShared(&typed.InputPath, "final", "answer")
	case *SubgraphNode:
		setScope(&typed.InputPath, defaultNodeOwner(typed, NodeTypeSubgraph), "input")
		setScope(&typed.OutputPath, defaultNodeOwner(typed, NodeTypeSubgraph), "output")
	case *SetFinalAnswerNode:
		setShared(&typed.OutputPath, "final", "answer")
		if typed.FromRequest {
			setShared(&typed.InputPath, "request", "input")
		}
	}
}

func defaultNodeOwner(targetNode Node, fallback string) string {
	if targetNode != nil {
		if id := strings.TrimSpace(targetNode.ID()); id != "" {
			return id
		}
	}
	return fallback
}

func compactGraphNodeConfig(config map[string]any) map[string]any {
	if len(config) == 0 {
		return nil
	}
	out := make(map[string]any, len(config))
	for key, value := range config {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(typed) == "" {
				continue
			}
			out[key] = typed
		case []string:
			if len(typed) == 0 {
				continue
			}
			out[key] = append([]string(nil), typed...)
		case map[string]string:
			if len(typed) == 0 {
				continue
			}
			cloned := make(map[string]string, len(typed))
			for k, v := range typed {
				cloned[k] = v
			}
			out[key] = cloned
		default:
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ensureNodeID(targetNode Node) {
	if targetNode == nil || strings.TrimSpace(targetNode.ID()) != "" {
		return
	}
	setter, ok := targetNode.(interface{ SetID(string) })
	if !ok {
		return
	}
	setter.SetID(defaultNodeID(targetNode))
}

func defaultNodeID(targetNode Node) string {
	if targetNode == nil {
		return "node"
	}
	if provider, ok := targetNode.(dsl.GraphNodeSpecProvider); ok {
		if nodeType := strings.TrimSpace(provider.GraphNodeSpec().Type); nodeType != "" {
			return nodeType
		}
	}
	if name := strings.TrimSpace(targetNode.Name()); name != "" {
		return name
	}
	return "node"
}
