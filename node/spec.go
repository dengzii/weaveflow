package node

import (
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

const (
	NodeTypeSubgraph           = "subgraph"
	NodeTypeConversationInput  = "conversation_input"
	NodeTypeContextReducer     = "context_reducer"
	NodeTypeLLM                = "llm"
	NodeTypeTools              = "tools"
	NodeTypeAgent              = "agent"
	NodeTypeEnvironmentContext = "environment_context"
	NodeTypeExplore            = "explore"
)

var (
	_ dsl.GraphNodeSpecProvider = (*SubgraphNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ConversationInputNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ContextReducerNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*LLMNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ToolsNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*AgentNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*EnvironmentContextNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ExploreNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*PlanGeneratorNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*PlanStepNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*PlanReviewNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*PlanFinalizeNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*SupervisorNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*SupervisorWorkerNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*SupervisorFinalizeNode)(nil)
)

func newGraphNodeSpec(base Base, nodeType string, config map[string]any, statePaths ...map[string]state.Path) dsl.GraphNodeSpec {
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
	case NodeTypePlanGenerator, NodeTypePlanStep, NodeTypePlanReview, NodeTypePlanFinalize:
		if port == "plan" {
			template = "shared.plan"
		} else if port == "execution" {
			template = "shared.execution"
		} else if port == "conversation" {
			template = "scopes.{node_id}.conversation"
		}
	case NodeTypeSupervisor, NodeTypeSupervisorWorker, NodeTypeSupervisorFinalize:
		if port == "supervisor" {
			template = "shared.supervisor"
		} else if port == "conversation" {
			template = "scopes.{node_id}.conversation"
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

func ApplyDefaultStatePaths(node Node) {
	if node == nil {
		return
	}
	if strings.TrimSpace(node.ID()) == "" {
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
	switch typed := node.(type) {
	case *ConversationInputNode:
		setShared(&typed.InputPath, "request", "input")
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeConversationInput), "conversation")
		setShared(&typed.PendingInputPath, "request", "pending_input")
	case *ContextReducerNode:
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeContextReducer), "conversation")
	case *LLMNode:
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeLLM), "conversation")
		setShared(&typed.OutputPath, "final", "answer")
	case *ToolsNode:
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeTools), "conversation")
	case *AgentNode:
		setShared(&typed.TaskPath, "request", "input")
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeAgent), "conversation")
		setShared(&typed.ResultPath, "final", "answer")
	case *ExploreNode:
		setShared(&typed.TaskPath, "request", "input")
		setScope(&typed.ParentConversationPath, defaultNodeOwner(typed, NodeTypeExplore), "parent_conversation")
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeExplore), "conversation")
		setShared(&typed.EnvironmentPath, "environment")
		setShared(&typed.ResultPath, "final", "answer")
	case *EnvironmentContextNode:
		setShared(&typed.EnvironmentPath, "environment")
	case *SubgraphNode:
		setScope(&typed.InputPath, defaultNodeOwner(typed, NodeTypeSubgraph), "input")
		setScope(&typed.OutputPath, defaultNodeOwner(typed, NodeTypeSubgraph), "output")
	case *PlanGeneratorNode:
		setShared(&typed.ObjectivePath, "request", "input")
		setShared(&typed.PlanPath, "plan")
		setShared(&typed.ExecutionPath, "execution")
	case *PlanStepNode:
		setShared(&typed.PlanPath, "plan")
		setShared(&typed.ExecutionPath, "execution")
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypePlanStep), "conversation")
	case *PlanReviewNode:
		setShared(&typed.PlanPath, "plan")
		setShared(&typed.ExecutionPath, "execution")
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypePlanReview), "conversation")
	case *PlanFinalizeNode:
		setShared(&typed.PlanPath, "plan")
		setShared(&typed.ResultPath, "final", "answer")
	case *SupervisorNode:
		setShared(&typed.ObjectivePath, "request", "input")
		setShared(&typed.SupervisorPath, "supervisor")
	case *SupervisorWorkerNode:
		setShared(&typed.SupervisorPath, "supervisor")
		setScope(&typed.ConversationPath, defaultNodeOwner(typed, NodeTypeSupervisorWorker), "conversation")
	case *SupervisorFinalizeNode:
		setShared(&typed.SupervisorPath, "supervisor")
		setShared(&typed.ResultPath, "final", "answer")
	case *SetFinalAnswerNode:
		setShared(&typed.OutputPath, "final", "answer")
		if typed.FromRequest {
			setShared(&typed.InputPath, "request", "input")
		}
	}
}

func defaultNodeOwner(node Node, fallback string) string {
	if node != nil {
		if id := strings.TrimSpace(node.ID()); id != "" {
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

func ensureNodeID(node Node) {
	if node == nil || strings.TrimSpace(node.ID()) != "" {
		return
	}
	setter, ok := node.(interface{ SetID(string) })
	if !ok {
		return
	}
	setter.SetID(defaultNodeID(node))
}

func defaultNodeID(node Node) string {
	if node == nil {
		return "node"
	}
	if provider, ok := node.(dsl.GraphNodeSpecProvider); ok {
		if nodeType := strings.TrimSpace(provider.GraphNodeSpec().Type); nodeType != "" {
			return nodeType
		}
	}
	if name := strings.TrimSpace(node.Name()); name != "" {
		return name
	}
	return "node"
}
