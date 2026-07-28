package plan

import (
	"fmt"

	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
)

func RegisterNodeTypes(target *registry.Registry) error {
	if target == nil {
		return fmt.Errorf("registry is nil")
	}
	definitions := []registry.NodeTypeDefinition{
		PlanGeneratorNodeTypeDefinition(),
		PlanStepNodeTypeDefinition(),
		PlanReviewNodeTypeDefinition(),
		PlanSynthesisNodeTypeDefinition(),
	}
	for _, definition := range definitions {
		if err := target.RegisterNodeTypeInGroup(basenode.NodeGroupOrchestration, definition); err != nil {
			return fmt.Errorf("register node type %q: %w", definition.Type, err)
		}
	}
	return nil
}
