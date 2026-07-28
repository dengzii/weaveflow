package builtin

import (
	"fmt"

	"github.com/dengzii/weaveflow/node"
	plannode "github.com/dengzii/weaveflow/node/plan"
	supervisornode "github.com/dengzii/weaveflow/node/supervisor"
	"github.com/dengzii/weaveflow/registry"
)

func RegisterCoreNodeTypes(r *registry.Registry) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if err := node.RegisterCoreNodeTypes(r); err != nil {
		return err
	}
	if err := plannode.RegisterNodeTypes(r); err != nil {
		return err
	}
	if err := supervisornode.RegisterNodeTypes(r); err != nil {
		return err
	}
	return registerCoreConditions(r)
}
