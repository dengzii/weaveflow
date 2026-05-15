package builtin

import (
	"fmt"
	"strings"

	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/registry"
	wfstate "weaveflow/state"
)

type moduleNodeBuilder func(*registry.BuildContext, dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error)

func adaptNodeBuilder(build moduleNodeBuilder) func(registry.NodeBuildContext, dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error) {
	return func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error) {
		if build == nil {
			return nil, fmt.Errorf("node builder is nil")
		}
		if concrete, ok := ctx.(*registry.BuildContext); ok {
			return build(concrete, spec)
		}
		if ctx == nil {
			return build(nil, spec)
		}
		options := ctx.BuildOptions()
		return build(&registry.BuildContext{
			InstanceConfig:       options.InstanceConfig,
			GraphResolver:        options.GraphResolver,
			OnContractDiagnostic: options.OnContractDiagnostic,
		}, spec)
	}
}

func canonicalContractPath(path string) string {
	return wfstate.NormalizeContractPath(path)
}

func scopedConversationPath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if field == "" {
		if scope == "" {
			return "conversation"
		}
		return "scopes." + scope
	}
	if scope == "" {
		return "conversation." + field
	}
	return "scopes." + scope + "." + field
}

func scopedStatePath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if scope == "" {
		return canonicalContractPath(field)
	}
	if field == "" {
		return "scopes." + scope
	}
	return "scopes." + scope + "." + field
}
