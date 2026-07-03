package builtin

import (
	"fmt"

	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
)

func RegisterCoreNodeTypes(r *registry.Registry) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if err := node.RegisterCoreNodeTypes(r); err != nil {
		return err
	}
	return registerCoreConditions(r)
}
