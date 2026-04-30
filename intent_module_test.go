package weaveflow

import (
	"context"
	"testing"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

type stubIntentModel struct {
	response string
	err      error
}

func (m stubIntentModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{
				Content: m.response,
			},
		},
	}, nil
}

func (m stubIntentModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return m.response, m.err
}

func TestIntentAnalyzerRegisteredInDefaultRegistry(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	if _, ok := registry.NodeTypes["intent_analyzer"]; !ok {
		t.Fatal("expected intent_analyzer node type to be registered")
	}
	if _, ok := registry.StateFields[fruntime.StateKeyIntent]; !ok {
		t.Fatal("expected intent state field to be registered")
	}
}

func TestIntentAnalyzerBuildsAndWritesIntentState(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()

	def := GraphDefinition{
		EntryPoint:  "intent",
		FinishPoint: "intent",
		Nodes: []GraphNodeSpec{
			{
				ID:   "intent",
				Type: "intent_analyzer",
				Config: map[string]any{
					"input_path":        "request.text",
					"intent_state_path": "analysis.intent",
					"intent_options":    []string{"search", "plan", "chat"},
				},
			},
		},
	}

	graph, err := registry.BuildGraph(def, &BuildContext{})
	if err != nil {
		t.Fatalf("build intent graph: %v", err)
	}

	svc := &fruntime.Services{
		Model: stubIntentModel{
			response: `{
  "label": "search",
  "confidence": 0.93,
  "reasoning": "The request asks to look up information.",
  "slots": {
    "topic": "browser automation"
  },
  "candidates": [
    {
      "label": "search",
      "confidence": 0.93,
      "reasoning": "Direct information lookup intent."
    },
    {
      "label": "plan",
      "confidence": 0.22,
      "reasoning": "Planning is secondary here."
    }
  ]
}`,
		},
	}
	ctx := fruntime.WithServices(context.Background(), svc)

	state := State{
		"request": map[string]any{
			"text": "Please look up browser automation options.",
		},
	}
	state, err = graph.Run(ctx, state)
	if err != nil {
		t.Fatalf("run intent graph: %v", err)
	}

	intent, ok := fruntime.ResolveStatePath(state, "analysis.intent")
	if !ok {
		t.Fatal("expected nested intent state to be written")
	}
	intentState, ok := intent.(map[string]any)
	if !ok {
		if typed, typedOK := intent.(State); typedOK {
			intentState = typed
		} else {
			t.Fatalf("expected intent state map, got %T", intent)
		}
	}
	if got := intentState["label"]; got != "search" {
		t.Fatalf("expected intent label search, got %#v", got)
	}
	if got := intentState["confidence"]; got != 0.93 {
		t.Fatalf("expected intent confidence 0.93, got %#v", got)
	}
	slots, ok := intentState["slots"].(map[string]any)
	if !ok || slots["topic"] != "browser automation" {
		t.Fatalf("unexpected intent slots: %#v", intentState["slots"])
	}
	candidates, ok := intentState["candidates"].([]map[string]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("unexpected intent candidates: %#v", intentState["candidates"])
	}
}

func TestResolveIntentAnalyzerStateContractUsesConfiguredPaths(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	contract, err := registry.ResolveNodeStateContract(GraphNodeSpec{
		ID:   "intent",
		Type: "intent_analyzer",
		Config: map[string]any{
			"input_path":        "request.input",
			"intent_state_path": "analysis.intent",
		},
	})
	if err != nil {
		t.Fatalf("resolve intent analyzer state contract: %v", err)
	}

	if len(contract.Fields) != 2 {
		t.Fatalf("expected 2 contract fields, got %#v", contract.Fields)
	}
	if contract.Fields[0].Path != "request.input" || contract.Fields[0].Mode != dsl.StateAccessRead {
		t.Fatalf("unexpected input contract field: %#v", contract.Fields[0])
	}
	if contract.Fields[1].Path != "analysis.intent" || contract.Fields[1].Mode != dsl.StateAccessWrite {
		t.Fatalf("unexpected intent output contract field: %#v", contract.Fields[1])
	}
}
