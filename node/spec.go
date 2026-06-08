package node

import (
	"fmt"
	"strings"

	"weaveflow/dsl"
)

const (
	NodeTypeMappedSubgraph     = "mapped_subgraph"
	NodeTypeHumanMessage       = "human_message"
	NodeTypeContextReducer     = "context_reducer"
	NodeTypeLLM                = "llm"
	NodeTypeTools              = "tools"
	NodeTypeAgent              = "agent"
	NodeTypeEnvironmentContext = "environment_context"
)

var (
	_ dsl.GraphNodeSpecProvider = (*MappedSubgraphNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*HumanMessageNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ContextReducerNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*LLMNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*ToolsNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*AgentNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*EnvironmentContextNode)(nil)
)

func newGraphNodeSpec(base Base, nodeType string, config map[string]any) dsl.GraphNodeSpec {
	spec := dsl.GraphNodeSpec{
		ID:          base.ID(),
		Name:        base.Name(),
		Type:        nodeType,
		Description: base.Description(),
		Config:      compactGraphNodeConfig(config),
	}
	if spec.Name == "" {
		spec.Name = spec.ID
	}
	return spec
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
			if strings.TrimSpace(typed) == "" && key != "state_scope" {
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

func EnsureNodeID(node Node) {
	if node == nil || strings.TrimSpace(node.ID()) != "" {
		return
	}
	setter, ok := node.(interface{ SetID(string) })
	if !ok {
		return
	}
	setter.SetID(defaultNodeID(node))
}

func AllocateNodeID(node Node, exists func(string) bool) string {
	base := defaultNodeID(node)
	if exists == nil || !exists(base) {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if !exists(candidate) {
			return candidate
		}
	}
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

func (n *MappedSubgraphNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"graph_ref":  n.GraphRef,
		"input_map":  pathMappingConfig(n.InputMappings),
		"output_map": pathMappingConfig(n.OutputMappings),
	}
	return newGraphNodeSpec(n.Base, NodeTypeMappedSubgraph, config)
}

func pathMappingConfig(mappings []PathMapping) map[string]string {
	if len(mappings) == 0 {
		return nil
	}
	config := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		if mapping.From.Empty() || mapping.To.Empty() {
			continue
		}
		config[mapping.From.String()] = mapping.To.String()
	}
	if len(config) == 0 {
		return nil
	}
	return config
}

func (n *HumanMessageNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope":       n.Scope(),
		"content":           n.Content,
		"interrupt_message": n.InterruptMessage,
	}
	return newGraphNodeSpec(n.Base, NodeTypeHumanMessage, config)
}

func (n *ContextReducerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope":     n.Scope(),
		"preserve_system": n.PreserveSystem,
	}
	if n.MaxMessages > 0 {
		config["max_messages"] = n.MaxMessages
	}
	if n.PreserveRecent >= 0 {
		config["preserve_recent"] = n.PreserveRecent
	}
	if strings.TrimSpace(n.SummaryPrefix) != "" {
		config["summary_prefix"] = n.SummaryPrefix
	}
	return newGraphNodeSpec(n.Base, NodeTypeContextReducer, config)
}

func (n *LLMNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope": n.Scope(),
		"tool_ids":    n.ToolIDs,
	}
	if n.PromptMaxChars > 0 {
		config["prompt_max_chars"] = n.PromptMaxChars
	}
	return newGraphNodeSpec(n.Base, NodeTypeLLM, config)
}

func (n *ToolsNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope": n.Scope(),
		"tool_ids":    n.ToolIDs,
		"parallel":    n.Parallel,
	}
	return newGraphNodeSpec(n.Base, NodeTypeTools, config)
}

func (n *AgentNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope":      n.Scope(),
		"tool_ids":         n.ToolIDs,
		"system_prompt":    n.SystemPrompt,
		"parallel":         n.Parallel,
		"tool_name":        n.ToolName,
		"tool_description": n.ToolDescription,
	}
	if !n.InputPath.Empty() {
		config["input_path"] = n.InputPath.String()
	}
	if !n.OutputPath.Empty() {
		config["output_path"] = n.OutputPath.String()
	}
	if n.MaxIterations > 0 {
		config["max_iterations"] = n.MaxIterations
	}
	if n.PromptMaxChars > 0 {
		config["prompt_max_chars"] = n.PromptMaxChars
	}
	return newGraphNodeSpec(n.Base, NodeTypeAgent, config)
}

func (n *EnvironmentContextNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"environment_state_path": n.EnvironmentStatePath,
		"workspace_root":         n.WorkspaceRoot,
		"include_git":            n.IncludeGit,
		"include_project":        n.IncludeProject,
	}
	if n.GitStatusLimit > 0 {
		config["git_status_limit"] = n.GitStatusLimit
	}
	return newGraphNodeSpec(n.Base, NodeTypeEnvironmentContext, config)
}
