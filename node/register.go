package node

import (
	"fmt"

	"github.com/dengzii/weaveflow/node/stateops"
	"github.com/dengzii/weaveflow/registry"
)

func RegisterCoreNodeTypes(r *registry.Registry) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}

	definitions := []registry.NodeTypeDefinition{
		SubgraphNodeTypeDefinition(),
		ConversationInputNodeTypeDefinition(),
		ContextReducerNodeTypeDefinition(),
		LLMNodeTypeDefinition(),
		ToolsNodeTypeDefinition(),
		AgentNodeTypeDefinition(),
		EnvironmentContextNodeTypeDefinition(),
		ExploreNodeTypeDefinition(),
		PlanGeneratorNodeTypeDefinition(),
		PlanStepNodeTypeDefinition(),
		PlanReviewNodeTypeDefinition(),
		PlanFinalizeNodeTypeDefinition(),
		SupervisorNodeTypeDefinition(),
		SupervisorWorkerNodeTypeDefinition(),
		SupervisorFinalizeNodeTypeDefinition(),
	}
	definitions = append(definitions, stateops.NodeTypeDefinitions()...)
	for _, def := range definitions {
		if err := r.RegisterNodeType(def); err != nil {
			return fmt.Errorf("register node type %q: %w", def.Type, err)
		}
	}
	return nil
}
