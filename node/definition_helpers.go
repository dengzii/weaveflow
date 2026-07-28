package node

import (
	"strings"

	"github.com/dengzii/weaveflow/dsl"
)

func applyNodeMetadata(base *Base, spec dsl.GraphNodeSpec) {
	ApplyNodeMetadata(base, spec)
}

func ApplyNodeMetadata(base *Base, spec dsl.GraphNodeSpec) {
	if base == nil {
		return
	}
	base.Spec.ID = spec.ID
	if strings.TrimSpace(spec.Name) != "" {
		base.Spec.Name = spec.Name
	}
	if strings.TrimSpace(spec.Description) != "" {
		base.Spec.Description = spec.Description
	}
}
