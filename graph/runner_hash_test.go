package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/graphbuild"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestNewGraphRunnerPopulatesGraphHashes(t *testing.T) {
	g := NewGraph(nil)
	mustAddNode(t, g, "input", func(context.Context, *state.Access) error {
		return nil
	})
	if err := g.SetEntryPoint("input"); err != nil {
		t.Fatalf("set entry point: %v", err)
	}
	if err := g.SetFinishPoint("input"); err != nil {
		t.Fatalf("set finish point: %v", err)
	}

	def, err := g.Definition()
	if err != nil {
		t.Fatalf("graph definition: %v", err)
	}
	graphHash, err := dsl.SemanticGraphHash(def)
	if err != nil {
		t.Fatalf("semantic graph hash: %v", err)
	}
	graphSnapshotHash, err := dsl.SnapshotGraphHash(def)
	if err != nil {
		t.Fatalf("snapshot graph hash: %v", err)
	}

	dir := t.TempDir()
	runner := mustNewGraphRunner(t,
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	if runner.GraphHash() != graphHash {
		t.Fatalf("runner graph hash = %q, want %q", runner.GraphHash(), graphHash)
	}
	if runner.GraphSnapshotHash() != graphSnapshotHash {
		t.Fatalf("runner graph snapshot hash = %q, want %q", runner.GraphSnapshotHash(), graphSnapshotHash)
	}

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}
	if run.GraphHash != graphHash {
		t.Fatalf("run graph hash = %q, want %q", run.GraphHash, graphHash)
	}
	if run.GraphSnapshotHash != graphSnapshotHash {
		t.Fatalf("run graph snapshot hash = %q, want %q", run.GraphSnapshotHash, graphSnapshotHash)
	}
}

func TestGraphRunnerRejectsResumeWhenGraphHashChanged(t *testing.T) {
	g := NewGraph(nil)
	mustAddNode(t, g, "input", func(context.Context, *state.Access) error { return nil })
	if err := g.SetEntryPoint("input"); err != nil {
		t.Fatalf("set entry point: %v", err)
	}
	if err := g.SetFinishPoint("input"); err != nil {
		t.Fatalf("set finish point: %v", err)
	}

	dir := t.TempDir()
	runner := mustNewGraphRunner(t,
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}
	if run.LastCheckpointID == "" {
		t.Fatal("run has no checkpoint")
	}
	snapshotRunner := mustNewGraphRunner(t, g, runner.ExecutionStore(), runner.CheckpointStore(), state.NewJSONStateCodec(""), runner.EventSink(), fruntime.WithGraphMetadata("", "", runner.GraphHash(), "sha256:changed-snapshot", runner.GraphSessionID()))
	if _, _, err := snapshotRunner.Resume(context.Background(), run.RunID, nil); err == nil || !strings.Contains(err.Error(), "graph snapshot hash mismatch") {
		t.Fatalf("Resume() error = %v, want graph snapshot hash mismatch", err)
	}
	runner = mustNewGraphRunner(t, g, runner.ExecutionStore(), runner.CheckpointStore(), state.NewJSONStateCodec(""), runner.EventSink(), fruntime.WithGraphMetadata("", "", "sha256:changed", runner.GraphSnapshotHash(), runner.GraphSessionID()))

	if _, _, err := runner.Resume(context.Background(), run.RunID, nil); err == nil || !strings.Contains(err.Error(), "graph hash mismatch") {
		t.Fatalf("Resume() error = %v, want graph hash mismatch", err)
	}
	checkpoints, err := runner.ListCheckpoints(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("ListCheckpoints() error = %v", err)
	}
	checkpointID := ""
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage != fruntime.CheckpointFinal {
			checkpointID = checkpoint.CheckpointID
			break
		}
	}
	if checkpointID == "" {
		t.Fatal("run has no resumable checkpoint")
	}
	if _, _, err := runner.ResumeFromCheckpoint(context.Background(), checkpointID, nil); err == nil || !strings.Contains(err.Error(), "graph hash mismatch") {
		t.Fatalf("ResumeFromCheckpoint() error = %v, want graph hash mismatch", err)
	}
}

func TestGraphRunnerRejectsUnsafeNodeResultBeforeCheckpoint(t *testing.T) {
	g := NewGraph(nil)
	mustAddNode(t, g, "input", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("invalid"), func() {})
	})
	if err := g.SetEntryPoint("input"); err != nil {
		t.Fatalf("set entry point: %v", err)
	}
	if err := g.SetFinishPoint("input"); err != nil {
		t.Fatalf("set finish point: %v", err)
	}
	dir := t.TempDir()
	runner := mustNewGraphRunner(t,
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	_, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), "node result cannot be safely cloned") || !strings.Contains(err.Error(), "opaque reference type func()") {
		t.Fatalf("Start() error = %v, want strict node result clone error", err)
	}
}

func TestBuiltGraphHashIncludesResolvedCapabilityVersion(t *testing.T) {
	t.Parallel()
	v1 := builtCapabilityGraphHash(t, "test.thread.v1")
	v2 := builtCapabilityGraphHash(t, "test.thread.v2")
	if v1 == v2 {
		t.Fatalf("resolved capability version did not change graph hash: %q", v1)
	}
}

func TestBuiltGraphHashPreservesCustomRegistrySemantics(t *testing.T) {
	t.Parallel()
	const capabilityID = "custom.thread.v2"
	reg := registry.NewRegistry()
	module := dsl.StateModuleDefinition{
		Name: builtin.ProtocolsModuleName, Version: builtin.ProtocolsModuleVersion,
		Capabilities: []dsl.StateCapabilityDefinition{{
			ID: capabilityID, Schema: dsl.JSONSchema{"type": "object"},
			Fields: []dsl.StateCapabilityFieldDefinition{{Name: "messages", Schema: dsl.JSONSchema{"type": "array"}, MergeStrategy: dsl.StateMergeReplace}},
		}},
	}
	if err := reg.RegisterStateModule(module); err != nil {
		t.Fatalf("register module: %v", err)
	}
	if err := reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: node.NodeTypeLLMTurn, StatePorts: []dsl.StatePortDefinition{{
			Name: "conversation", Required: true, Capability: capabilityID,
			Contract: dsl.RelativeStateContract{Fields: []dsl.RelativeStateFieldRef{{Path: "messages", Mode: dsl.StateAccessRead}}},
		}}},
		Build: func(_ *registry.BuildContext, spec registry.ResolvedNodeSpec) (core.Node, error) {
			return node.NewFuncNode(node.Spec{ID: spec.Spec.ID}, func(core.Context, *state.Access) (core.NodeResult, error) { return core.Success(), nil }), nil
		},
	}); err != nil {
		t.Fatalf("register node: %v", err)
	}
	definition := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: []dsl.StateModuleRef{{Name: module.Name, Version: module.Version}},
		EntryPoint:   "llm",
		FinishPoint:  "llm",
		Nodes: []dsl.GraphNodeSpec{{
			ID: "llm", Type: node.NodeTypeLLMTurn,
			State: map[string]dsl.StateBinding{"conversation": {Path: "shared.thread"}},
		}},
	}
	workflow, err := NewBuilder(reg).Build(definition, nil)
	if err != nil {
		t.Fatalf("BuildGraph(): %v", err)
	}
	got, err := workflow.SemanticHash()
	if err != nil {
		t.Fatalf("semantic hash: %v", err)
	}
	want, err := dsl.SemanticGraphHashWithStateBindings(definition, workflow.stateBindingSemantics)
	if err != nil {
		t.Fatalf("custom semantic hash: %v", err)
	}
	if got != want {
		t.Fatalf("semantic hash = %q, want custom registry hash %q", got, want)
	}
	defaultResolved, err := graphbuild.ResolveGraphBindings(definition, builtin.NewDefaultRegistry())
	if err != nil {
		t.Fatalf("resolve default bindings: %v", err)
	}
	defaultHash, err := dsl.SemanticGraphHashWithStateBindings(definition, graphbuild.StateBindingSemantics(defaultResolved))
	if err != nil {
		t.Fatalf("default semantic hash: %v", err)
	}
	if got == defaultHash {
		t.Fatalf("custom registry semantics were replaced by the default registry: %q", got)
	}
}

func builtCapabilityGraphHash(t *testing.T, capabilityID string) string {
	t.Helper()
	reg := registry.NewRegistry()
	module := dsl.StateModuleDefinition{
		Name: "test.protocols", Version: "1",
		Capabilities: []dsl.StateCapabilityDefinition{
			{ID: "test.thread.v1", Schema: dsl.JSONSchema{"type": "object"}, Fields: []dsl.StateCapabilityFieldDefinition{{Name: "value", Schema: dsl.JSONSchema{"type": "string"}, MergeStrategy: dsl.StateMergeReplace}}},
			{ID: "test.thread.v2", Schema: dsl.JSONSchema{"type": "object"}, Fields: []dsl.StateCapabilityFieldDefinition{{Name: "value", Schema: dsl.JSONSchema{"type": "string"}, MergeStrategy: dsl.StateMergeReplace}}},
		},
	}
	if err := reg.RegisterStateModule(module); err != nil {
		t.Fatalf("register module: %v", err)
	}
	if err := reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "consumer", StatePorts: []dsl.StatePortDefinition{{
			Name: "thread", Required: true, Capability: capabilityID,
			Contract: dsl.RelativeStateContract{Fields: []dsl.RelativeStateFieldRef{{Path: "value", Mode: dsl.StateAccessRead}}},
		}}},
		Build: func(_ *registry.BuildContext, spec registry.ResolvedNodeSpec) (core.Node, error) {
			return node.NewFuncNode(node.Spec{ID: spec.Spec.ID}, func(core.Context, *state.Access) (core.NodeResult, error) { return core.Success(), nil }), nil
		},
	}); err != nil {
		t.Fatalf("register node: %v", err)
	}
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: []dsl.StateModuleRef{{Name: module.Name, Version: module.Version}},
		EntryPoint:   "consumer",
		FinishPoint:  "consumer",
		Nodes: []dsl.GraphNodeSpec{{
			ID: "consumer", Type: "consumer", State: map[string]dsl.StateBinding{"thread": {Path: "shared.thread"}},
		}},
	}
	g, err := NewBuilder(reg).Build(def, nil)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, g, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
	return runner.GraphHash()
}
