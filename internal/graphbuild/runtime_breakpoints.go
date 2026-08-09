package graphbuild

import (
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/runtime"
)

func RuntimeBreakpoints(options dsl.RunDebugOptions) []runtime.Breakpoint {
	effective := options.EffectiveBreakpoints()
	breakpoints := make([]runtime.Breakpoint, 0, len(effective))
	for _, breakpoint := range effective {
		enabled := breakpoint.Enabled == nil || *breakpoint.Enabled
		breakpoints = append(breakpoints, runtime.Breakpoint{
			ID:      breakpoint.ID,
			NodeID:  breakpoint.NodeID,
			Stage:   breakpoint.Stage,
			Enabled: enabled,
		})
	}
	return breakpoints
}
