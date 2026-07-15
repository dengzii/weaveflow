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
		State:       graphStateBindings(statePaths...),
	}
	if spec.Name == "" {
		spec.Name = spec.ID
	}
	return spec
}

func graphStateBindings(statePaths ...map[string]state.Path) map[string]dsl.StateBinding {
	if len(statePaths) == 0 || len(statePaths[0]) == 0 {
		return nil
	}
	bindings := make(map[string]dsl.StateBinding, len(statePaths[0]))
	for name, path := range statePaths[0] {
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
