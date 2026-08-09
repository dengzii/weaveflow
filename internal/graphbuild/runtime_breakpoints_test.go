package graphbuild

import (
	"testing"

	"github.com/dengzii/weaveflow/dsl"
)

func TestRuntimeBreakpointsConvertsEffectiveDSLBreakpoints(t *testing.T) {
	t.Parallel()
	disabled := false
	breakpoints := RuntimeBreakpoints(dsl.RunDebugOptions{
		Breakpoints: []dsl.DebugBreakpoint{{ID: "custom", NodeID: " review ", Stage: dsl.DebugBreakpointAfterNode, Enabled: &disabled}},
		PauseBefore: []string{" start ", "review"},
		PauseAfter:  []string{"review"},
	})

	if len(breakpoints) != 3 {
		t.Fatalf("breakpoints = %#v", breakpoints)
	}
	if breakpoints[0].ID != "custom" || breakpoints[0].NodeID != "review" || breakpoints[0].Stage != dsl.DebugBreakpointAfterNode || breakpoints[0].Enabled {
		t.Fatalf("explicit breakpoint = %#v", breakpoints[0])
	}
	if breakpoints[1].NodeID != "start" || breakpoints[1].Stage != dsl.DebugBreakpointBeforeNode || !breakpoints[1].Enabled {
		t.Fatalf("pause_before breakpoint = %#v", breakpoints[1])
	}
	if breakpoints[2].NodeID != "review" || breakpoints[2].Stage != dsl.DebugBreakpointBeforeNode || !breakpoints[2].Enabled {
		t.Fatalf("deduplicated review breakpoint = %#v", breakpoints[2])
	}
}
