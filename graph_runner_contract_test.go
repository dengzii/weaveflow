package weaveflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

type contractProbeNode struct {
	id      string
	spec    dsl.GraphNodeSpec
	mutate  func(State)
	inspect func(State) string
}

func (n contractProbeNode) ID() string          { return n.id }
func (n contractProbeNode) Name() string        { return n.id }
func (n contractProbeNode) Description() string { return "probe state contract runner behavior" }
func (n contractProbeNode) Invoke(ctx context.Context, state State) (State, error) {
	_ = ctx
	if state == nil {
		state = State{}
	}
	if n.inspect != nil {
		state["result"] = n.inspect(state)
	}
	if n.mutate != nil {
		n.mutate(state)
	}
	return state, nil
}

func (n contractProbeNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return n.spec
}

func registerContractProbeNodeType(registry *Registry, contract dsl.StateContract, mutate func(State), inspect func(State) string) {
	registry.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "contract_probe",
			Description: "Test node for runner state contract execution.",
			ConfigSchema: JSONSchema{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			_ = spec
			return contract.Clone(), nil
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			_ = ctx
			return contractProbeNode{
				id:      spec.ID,
				spec:    spec,
				mutate:  mutate,
				inspect: inspect,
			}, nil
		},
	})
}

func newContractTestRunner(t *testing.T, graph *Graph) *fruntime.GraphRunner {
	t.Helper()

	baseDir := t.TempDir()
	runner := NewGraphRunner(
		graph,
		fruntime.NewFileExecutionStore(baseDir),
		fruntime.NewFileCheckpointStore(baseDir),
		fruntime.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(baseDir),
	)
	runner.ArtifactStore = fruntime.NewFileArtifactStore(baseDir)
	runner.ContractValidation = fruntime.ContractValidationStrict
	return runner
}

type contractCaptureLLMModel struct {
	lastMessages []llms.MessageContent
}

func (m *contractCaptureLLMModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	_ = ctx
	_ = options
	m.lastMessages = append([]llms.MessageContent(nil), messages...)
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{Content: "runner reply"},
		},
	}, nil
}

func (m *contractCaptureLLMModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	_ = ctx
	_ = prompt
	_ = options
	return "", nil
}

func TestGraphRunnerProjectsNodeInputByContract(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "topic", Mode: dsl.StateAccessRead},
				{Path: "result", Mode: dsl.StateAccessWrite, Required: true},
			},
		},
		nil,
		func(state State) string {
			if _, ok := state["secret"]; ok {
				return "leaked"
			}
			if state["topic"] == "weather" {
				return "clean"
			}
			return "missing"
		},
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "probe",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "probe", Type: "contract_probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, finalState, err := runner.Start(context.Background(), State{
		"topic":  "weather",
		"secret": "hidden",
	})
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("expected completed run, got %q", run.Status)
	}
	if got := finalState["result"]; got != "clean" {
		t.Fatalf("expected projected input to hide secret, got %#v", finalState)
	}
	if got := finalState["secret"]; got != "hidden" {
		t.Fatalf("expected merge to preserve full state, got %#v", finalState)
	}
}

func TestGraphRunnerRejectsUndeclaredPatchWrite(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "result", Mode: dsl.StateAccessWrite},
			},
		},
		func(state State) {
			state["secret"] = "mutated"
		},
		func(state State) string {
			return "ok"
		},
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "probe",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "probe", Type: "contract_probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, _, err := runner.Start(context.Background(), State{})
	if err == nil {
		t.Fatal("expected undeclared patch write to fail")
	}
	if !strings.Contains(err.Error(), `not declared as writable`) {
		t.Fatalf("expected undeclared write error, got %v", err)
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("expected failed run, got %q", run.Status)
	}
}

func TestGraphRunnerRejectsMissingRequiredRead(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "topic", Mode: dsl.StateAccessRead, Required: true},
				{Path: "result", Mode: dsl.StateAccessWrite},
			},
		},
		nil,
		func(state State) string {
			return "ok"
		},
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "probe",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "probe", Type: "contract_probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, _, err := runner.Start(context.Background(), State{})
	if err == nil {
		t.Fatal("expected missing required read to fail")
	}
	if !strings.Contains(err.Error(), `requires input path "shared.topic"`) {
		t.Fatalf("expected required read error, got %v", err)
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("expected failed run, got %q", run.Status)
	}
}

func TestGraphRunnerRejectsMissingRequiredWrite(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "result", Mode: dsl.StateAccessWrite, Required: true},
			},
		},
		nil,
		nil,
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "probe",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "probe", Type: "contract_probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, _, err := runner.Start(context.Background(), State{})
	if err == nil {
		t.Fatal("expected missing required write to fail")
	}
	if !strings.Contains(err.Error(), `must write path "shared.result"`) {
		t.Fatalf("expected required write error, got %v", err)
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("expected failed run, got %q", run.Status)
	}
}

func TestGraphRunnerIteratesWithRuntimePrivateStateContracts(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerCollectIteratorItemNodeType(registry)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "loop",
		FinishPoint: "loop",
		Nodes: []GraphNodeSpec{
			{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key":      "payload.items",
					"max_iterations": 2,
					"continue_to":    "collect",
					"done_to":        dsl.EndNodeRef,
				},
			},
			{
				ID:   "collect",
				Type: "collect_iterator_item",
				Config: map[string]any{
					"iterator_node_id": "loop",
					"target_key":       "results",
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "collect", To: "loop"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, finalState, err := runner.Start(context.Background(), State{
		"payload": map[string]any{
			"items": []any{"alpha", "beta", "gamma"},
		},
	})
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("expected completed run, got %q", run.Status)
	}

	results, ok := finalState["results"].([]string)
	if !ok || len(results) != 2 || results[0] != "alpha" || results[1] != "beta" {
		t.Fatalf("expected collected iterator results, got %#v", finalState["results"])
	}

	runtimeState := finalState.Namespace(nodes.IteratorStateNamespace)
	if runtimeState == nil {
		t.Fatalf("expected runtime namespace in final state, got %#v", finalState)
	}
	loopState, ok := runtimeState["loop"].(map[string]any)
	if !ok {
		if typed, ok := runtimeState["loop"].(State); ok {
			loopState = typed
		} else {
			t.Fatalf("expected runtime loop state map, got %#v", runtimeState["loop"])
		}
	}
	if loopState["done"] != true {
		t.Fatalf("expected iterator to finish, got %#v", loopState)
	}
}

func TestGraphRunnerProjectsRootConversationFallbackForScopedLLMNode(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "model",
		FinishPoint: "model",
		Nodes: []GraphNodeSpec{
			{
				ID:   "model",
				Type: "llm",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	model := &contractCaptureLLMModel{}
	initial := State{}
	initial.Conversation("").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hello from root"),
	})
	initial.Conversation("").SetMaxIterations(16)

	runner := newContractTestRunner(t, graph)
	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{Model: model})
	run, finalState, err := runner.Start(ctx, initial)
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("expected completed run, got %q", run.Status)
	}
	if len(model.lastMessages) != 1 {
		t.Fatalf("expected model to receive one message, got %#v", model.lastMessages)
	}
	textPart, ok := model.lastMessages[0].Parts[0].(llms.TextContent)
	if !ok || textPart.Text != "hello from root" {
		t.Fatalf("expected model to receive root fallback message, got %#v", model.lastMessages)
	}

	messages := finalState.Conversation("agent").Messages()
	if len(messages) != 2 {
		t.Fatalf("expected scoped conversation to contain input plus reply, got %#v", messages)
	}
	first, ok := messages[0].Parts[0].(llms.TextContent)
	if !ok || first.Text != "hello from root" {
		t.Fatalf("unexpected first scoped message: %#v", messages[0])
	}
	last, ok := messages[1].Parts[0].(llms.TextContent)
	if !ok || last.Text != "runner reply" {
		t.Fatalf("unexpected llm reply: %#v", messages[1])
	}
	if got := finalState.Conversation("agent").MaxIterations(); got != 16 {
		t.Fatalf("expected scoped max_iterations to inherit root fallback, got %d", got)
	}
}

func TestGraphRunnerPublishesContractWarningEventsAndArtifacts(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerStaticContractNodeType(registry, "wildcard_runner", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "*", Mode: dsl.StateAccessReadWrite},
		},
	})
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "topic", Mode: dsl.StateAccessRead},
				{Path: "result", Mode: dsl.StateAccessWrite, Required: true},
			},
		},
		nil,
		func(state State) string {
			return state["topic"].(string)
		},
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "wild",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "wild", Type: "wildcard_runner"},
			{ID: "probe", Type: "contract_probe"},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "wild", To: "probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, _, err := runner.Start(context.Background(), State{
		"topic":  "weather",
		"secret": "hidden",
	})
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}

	events, err := runner.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	foundWarning := false
	for _, event := range events {
		if event.Type != fruntime.EventWarning {
			continue
		}
		var warning fruntime.WarningRecord
		if err := json.Unmarshal(event.Payload, &warning); err != nil {
			t.Fatalf("decode warning payload: %v", err)
		}
		if warning.Code == "wildcard_contract" {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected wildcard_contract warning event, got %#v", events)
	}

	artifacts, err := runner.ListArtifacts(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	foundTypes := map[string]bool{}
	var inputViewRef fruntime.ArtifactRef
	for _, ref := range artifacts {
		foundTypes[ref.Type] = true
		if ref.Type == "contract.input_view" && ref.NodeID == "probe" {
			inputViewRef = ref
		}
	}
	for _, want := range []string{"contract.input_view", "contract.output_patch", "contract.merged_state"} {
		if !foundTypes[want] {
			t.Fatalf("expected artifact type %q, got %#v", want, artifacts)
		}
	}
	if inputViewRef.ID == "" {
		t.Fatalf("expected probe input view artifact, got %#v", artifacts)
	}

	artifact, err := runner.LoadArtifact(context.Background(), inputViewRef)
	if err != nil {
		t.Fatalf("load input view artifact: %v", err)
	}
	var payload struct {
		Stage    string                 `json:"stage"`
		Snapshot fruntime.StateSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(artifact.Data, &payload); err != nil {
		t.Fatalf("decode input view artifact: %v", err)
	}
	if payload.Stage != "input_view" {
		t.Fatalf("expected input_view stage, got %q", payload.Stage)
	}
	if _, ok := payload.Snapshot.Shared["topic"]; !ok {
		t.Fatalf("expected projected snapshot to include topic, got %#v", payload.Snapshot.Shared)
	}
	if _, ok := payload.Snapshot.Shared["secret"]; ok {
		t.Fatalf("projected input view leaked secret: %#v", payload.Snapshot.Shared)
	}
}
