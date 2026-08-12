package agents

import (
	"fmt"

	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/node/agents/agent"
	"github.com/dengzii/weaveflow/node/agents/claude"
	"github.com/dengzii/weaveflow/node/agents/codex"
	"github.com/dengzii/weaveflow/registry"
)

func RegisterNodeTypes(target *registry.Registry) error {
	if target == nil {
		return fmt.Errorf("registry is nil")
	}
	definitions := []registry.NodeTypeDefinition{
		agent.NodeTypeDefinition(),
		claude.NodeTypeDefinition(),
		codex.NodeTypeDefinition(),
	}
	for _, definition := range definitions {
		if err := target.RegisterNodeTypeInGroup(basenode.NodeGroupAgents, definition); err != nil {
			return fmt.Errorf("register node type %q: %w", definition.Type, err)
		}
	}
	return nil
}
