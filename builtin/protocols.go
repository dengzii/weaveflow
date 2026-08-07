package builtin

import (
	"fmt"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	executioncap "github.com/dengzii/weaveflow/capability/execution"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
)

const (
	ProtocolsModuleName    = "weaveflow.protocols"
	ProtocolsModuleVersion = "1"
)

func ProtocolsStateModuleDefinition() dsl.StateModuleDefinition {
	return dsl.StateModuleDefinition{
		Name:    ProtocolsModuleName,
		Version: ProtocolsModuleVersion,
		Fields: []dsl.StateFieldDefinition{
			{Path: "shared.request.input", Description: "Default graph request input.", Schema: dsl.JSONSchema{"type": "string"}},
			{Path: "shared.request.metadata", Description: "Default graph request metadata.", Schema: dsl.JSONSchema{"type": "object"}},
			{Path: "shared.trigger", Description: "Trigger metadata such as type, id, and payload.", Schema: dsl.JSONSchema{"type": "object"}},
			{Path: "shared.environment", Description: "Workspace and project environment context.", Schema: dsl.JSONSchema{"type": "object"}},
			{Path: "shared.final.answer", Description: "Default final answer output.", Schema: dsl.JSONSchema{"type": "string"}},
		},
		Capabilities: []dsl.StateCapabilityDefinition{
			conversationcap.Definition(),
			plancap.Definition(),
			supervisorcap.Definition(),
			executioncap.Definition(),
		},
	}
}

func registerConversationModule(target *registry.Registry) error {
	if target == nil {
		return fmt.Errorf("registry is nil")
	}
	return target.RegisterStateModule(ProtocolsStateModuleDefinition())
}
