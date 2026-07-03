package node

import (
	"fmt"

	"github.com/dengzii/weaveflow/registry"
)

func RegisterCoreNodeTypes(r *registry.Registry) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}

	for _, def := range []registry.NodeTypeDefinition{
		MappedSubgraphNodeTypeDefinition(),
		HumanMessageNodeTypeDefinition(),
		ContextReducerNodeTypeDefinition(),
		LLMNodeTypeDefinition(),
		ToolsNodeTypeDefinition(),
		AgentNodeTypeDefinition(),
		EnvironmentContextNodeTypeDefinition(),
	} {
		if err := r.RegisterNodeType(def); err != nil {
			return fmt.Errorf("register node type %q: %w", def.Type, err)
		}
	}
	return nil
}
