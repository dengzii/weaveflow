package node

import (
	"fmt"

	"github.com/dengzii/weaveflow/node/stateops"
	"github.com/dengzii/weaveflow/registry"
)

const (
	NodeGroupInputContext  = "Input & Context"
	NodeGroupModelTools    = "Model & Tools"
	NodeGroupAgents        = "Agents"
	NodeGroupOrchestration = "Orchestration"
	NodeGroupOutput        = "Output"
	NodeGroupState         = "State"
)

func RegisterCoreNodeTypes(r *registry.Registry) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}

	definitions := []struct {
		group      string
		definition registry.NodeTypeDefinition
	}{
		{NodeGroupOrchestration, SubgraphNodeTypeDefinition()},
		{NodeGroupInputContext, UserInputNodeTypeDefinition()},
		{NodeGroupInputContext, ConversationMessageNodeTypeDefinition()},
		{NodeGroupInputContext, ContextReducerNodeTypeDefinition()},
		{NodeGroupModelTools, LLMTurnNodeTypeDefinition()},
		{NodeGroupModelTools, TextGenerationNodeTypeDefinition()},
		{NodeGroupModelTools, ToolExecutionNodeTypeDefinition()},
		{NodeGroupInputContext, EnvironmentContextNodeTypeDefinition()},
		{NodeGroupAgents, ExploreAgentNodeTypeDefinition()},
		{NodeGroupOutput, ChatReplyNodeTypeDefinition()},
	}
	for _, item := range definitions {
		if err := r.RegisterNodeTypeInGroup(item.group, item.definition); err != nil {
			return fmt.Errorf("register node type %q: %w", item.definition.Type, err)
		}
	}
	for _, definition := range stateops.NodeTypeDefinitions() {
		if err := r.RegisterNodeTypeInGroup(NodeGroupState, definition); err != nil {
			return fmt.Errorf("register node type %q: %w", definition.Type, err)
		}
	}
	return nil
}
