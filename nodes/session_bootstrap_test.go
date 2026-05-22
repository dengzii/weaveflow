package nodes

import (
	"context"
	"testing"
	"time"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

// asStringMap accepts either map[string]any or wfstate.State, mirroring what
// downstream consumers (e.g. CostBudgetGuard) do with nested state buckets.
func asStringMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case wfstate.State:
		return map[string]any(typed)
	default:
		return nil
	}
}

func TestSessionBootstrapNodeInitializesEmptyScopedState(t *testing.T) {
	t.Parallel()

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "Summarize the repository status."
	node.SystemPrompt = "You are a concise engineering agent."
	node.MaxIterations = 4
	node.AgentProfile = map[string]any{
		"name": "falcon",
		"mode": "general",
	}
	node.RequestMetadata = map[string]any{
		"tenant_id": "tenant-1",
		"user_id":   "user-1",
	}
	node.ToolPolicy = map[string]any{
		"allowed_tools": []any{"calculator", "current_time"},
	}

	state, err := runTestNode(t, node, context.Background(), nil)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}
	if state == nil {
		t.Fatal("expected initialized state")
	}

	conversation := state.Conversation("agent")
	messages := conversation.Messages()
	if len(messages) != 2 {
		t.Fatalf("expected system and human messages, got %#v", messages)
	}
	if messages[0].Role != llms.ChatMessageTypeSystem || extractText(messages[0]) != "You are a concise engineering agent." {
		t.Fatalf("unexpected system message: %#v", messages[0])
	}
	if messages[1].Role != llms.ChatMessageTypeHuman || extractText(messages[1]) != "Summarize the repository status." {
		t.Fatalf("unexpected human message: %#v", messages[1])
	}
	if got := conversation.MaxIterations(); got != 4 {
		t.Fatalf("expected max iterations 4, got %d", got)
	}

	request := state.Get(wfstate.StateKeyRequest)
	if request == nil || request["input"] != "Summarize the repository status." {
		t.Fatalf("expected normalized request input, got %#v", request)
	}
	metadata, ok := request["metadata"].(map[string]any)
	if !ok || metadata["tenant_id"] != "tenant-1" || metadata["user_id"] != "user-1" {
		t.Fatalf("unexpected request metadata: %#v", request["metadata"])
	}

	agent := state.Get(wfstate.StateKeyAgent)
	profile, ok := agent["profile"].(map[string]any)
	if !ok || profile["name"] != "falcon" || profile["mode"] != "general" {
		t.Fatalf("unexpected agent profile: %#v", agent["profile"])
	}

	toolPolicy := state.Get(wfstate.StateKeyToolPolicy)
	allowed, ok := toolPolicy["allowed_tools"].([]any)
	if !ok || len(allowed) != 2 || allowed[0] != "calculator" {
		t.Fatalf("unexpected tool policy: %#v", toolPolicy)
	}
}

func TestSessionBootstrapNodeUsesConfiguredInputPath(t *testing.T) {
	t.Parallel()

	state := wfstate.State{
		"incoming": map[string]any{
			"text": "Use the local calculator.",
		},
	}

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.InputPath = "incoming.text"

	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	if got := next.Get(wfstate.StateKeyRequest)["input"]; got != "Use the local calculator." {
		t.Fatalf("expected input path value to become request input, got %#v", got)
	}
	messages := next.Conversation("agent").Messages()
	if len(messages) != 1 || messages[0].Role != llms.ChatMessageTypeHuman || extractText(messages[0]) != "Use the local calculator." {
		t.Fatalf("unexpected conversation messages: %#v", messages)
	}
}

func TestSessionBootstrapNodePreservesExistingScopedConversation(t *testing.T) {
	t.Parallel()

	state := wfstate.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "Existing input"),
	})

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "New input"
	node.SystemPrompt = "Do not duplicate"
	node.MaxIterations = 2

	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	messages := next.Conversation("agent").Messages()
	if len(messages) != 2 {
		t.Fatalf("expected system prompt to be inserted ahead of preserved conversation, got %#v", messages)
	}
	if messages[0].Role != llms.ChatMessageTypeSystem || extractText(messages[0]) != "Do not duplicate" {
		t.Fatalf("unexpected inserted system prompt: %#v", messages[0])
	}
	if messages[1].Role != llms.ChatMessageTypeHuman || extractText(messages[1]) != "Existing input" {
		t.Fatalf("expected existing conversation to be preserved, got %#v", messages)
	}
	if got := next.Get(wfstate.StateKeyRequest)["input"]; got != "New input" {
		t.Fatalf("expected request input to still be normalized, got %#v", got)
	}
	if got := next.Conversation("agent").MaxIterations(); got != 2 {
		t.Fatalf("expected max iterations 2, got %d", got)
	}
}

func TestSessionBootstrapNodeDoesNotDuplicateExistingSystemPrompt(t *testing.T) {
	t.Parallel()

	state := wfstate.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "Stay concise."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Existing input"),
	})

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "New input"
	node.SystemPrompt = "Stay concise."

	_, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	messages := state.Conversation("agent").Messages()
	if len(messages) != 2 {
		t.Fatalf("expected existing system prompt to be reused, got %#v", messages)
	}
	if messages[0].Role != llms.ChatMessageTypeSystem || extractText(messages[0]) != "Stay concise." {
		t.Fatalf("unexpected system prompt: %#v", messages[0])
	}
}

func TestSessionBootstrapNodeInjectsRuntimeMetadata(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 5, 22, 9, 30, 0, 0, time.UTC)
	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "hello"
	node.RequestMetadata = map[string]any{"tenant_id": "tenant-1"}
	node.NowFunc = func() time.Time { return fixedNow }

	ctx := fruntime.WithRunnerMetadata(context.Background(), fruntime.RunnerMetadata{RunID: "run-123"})
	next, err := runTestNode(t, node, ctx, wfstate.State{})
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	metadata, ok := next.Get(wfstate.StateKeyRequest)["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %#v", next.Get(wfstate.StateKeyRequest)["metadata"])
	}
	if metadata["tenant_id"] != "tenant-1" {
		t.Fatalf("expected config metadata preserved, got %#v", metadata["tenant_id"])
	}
	if metadata["run_id"] != "run-123" {
		t.Fatalf("expected run_id injected from runner metadata, got %#v", metadata["run_id"])
	}
	if metadata["now"] != fixedNow.Format(time.RFC3339) {
		t.Fatalf("expected now from NowFunc, got %#v", metadata["now"])
	}
}

func TestSessionBootstrapNodeConfigMetadataOverridesRuntime(t *testing.T) {
	t.Parallel()

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "hello"
	node.RequestMetadata = map[string]any{
		"run_id": "explicit-run",
		"now":    "2000-01-01T00:00:00Z",
	}
	node.NowFunc = func() time.Time { return time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC) }

	ctx := fruntime.WithRunnerMetadata(context.Background(), fruntime.RunnerMetadata{RunID: "runtime-run"})
	next, err := runTestNode(t, node, ctx, wfstate.State{})
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	metadata, _ := next.Get(wfstate.StateKeyRequest)["metadata"].(map[string]any)
	if metadata["run_id"] != "explicit-run" {
		t.Fatalf("expected config run_id to win, got %#v", metadata["run_id"])
	}
	if metadata["now"] != "2000-01-01T00:00:00Z" {
		t.Fatalf("expected config now to win, got %#v", metadata["now"])
	}
}

func TestSessionBootstrapNodeRendersConfigInputAndSystemPrompt(t *testing.T) {
	t.Parallel()

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "Summarize work for {{agent.profile.name}} as of {{request.metadata.now}}."
	node.SystemPrompt = "You are {{agent.profile.name}}, a {{agent.profile.role}}."
	node.AgentProfile = map[string]any{
		"name": "falcon",
		"role": "researcher",
	}
	node.NowFunc = func() time.Time { return time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC) }

	next, err := runTestNode(t, node, context.Background(), wfstate.State{})
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	gotInput := next.Get(wfstate.StateKeyRequest)["input"]
	wantInput := "Summarize work for falcon as of 2026-05-22T00:00:00Z."
	if gotInput != wantInput {
		t.Fatalf("expected rendered input %q, got %#v", wantInput, gotInput)
	}

	messages := next.Conversation("agent").Messages()
	if len(messages) != 2 {
		t.Fatalf("expected system+human messages, got %#v", messages)
	}
	if got := extractText(messages[0]); got != "You are falcon, a researcher." {
		t.Fatalf("expected rendered system prompt, got %q", got)
	}
}

func TestSessionBootstrapNodeUnresolvedTemplateKeysPassThroughAndAreReported(t *testing.T) {
	t.Parallel()

	captured := map[fruntime.EventType][]map[string]any{}
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(eventType fruntime.EventType, payload any) error {
		if p, ok := payload.(map[string]any); ok {
			captured[eventType] = append(captured[eventType], p)
		}
		return nil
	})

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "Hello {{missing.key}} and {{also.missing}}."

	next, err := runTestNode(t, node, ctx, wfstate.State{})
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	// Unresolved placeholders are left intact so the breakage is visible.
	if got := next.Get(wfstate.StateKeyRequest)["input"]; got != "Hello {{missing.key}} and {{also.missing}}." {
		t.Fatalf("expected unresolved placeholders preserved, got %#v", got)
	}

	events := captured[fruntime.EventNodeCustom]
	if len(events) != 1 {
		t.Fatalf("expected one custom event, got %d", len(events))
	}
	unresolved, ok := events[0]["unresolved_template_keys"].([]string)
	if !ok {
		t.Fatalf("expected unresolved_template_keys in payload, got %#v", events[0])
	}
	if len(unresolved) != 2 || unresolved[0] != "also.missing" || unresolved[1] != "missing.key" {
		t.Fatalf("expected sorted unresolved keys, got %#v", unresolved)
	}
}

func TestSessionBootstrapNodeDoesNotRenderInputPathValues(t *testing.T) {
	t.Parallel()

	state := wfstate.State{
		"incoming": map[string]any{
			"text": "User typed {{request.metadata.run_id}} literally.",
		},
	}

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.InputPath = "incoming.text"

	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	want := "User typed {{request.metadata.run_id}} literally."
	if got := next.Get(wfstate.StateKeyRequest)["input"]; got != want {
		t.Fatalf("expected raw user input preserved (no templating), got %#v", got)
	}
}

func TestSessionBootstrapNodeEmitsObservabilityFields(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(eventType fruntime.EventType, payload any) error {
		if eventType == fruntime.EventNodeCustom {
			if p, ok := payload.(map[string]any); ok {
				captured = p
			}
		}
		return nil
	})
	ctx = fruntime.WithRunnerMetadata(ctx, fruntime.RunnerMetadata{RunID: "run-xyz"})

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "do the thing"
	node.SystemPrompt = "Be concise."
	node.MaxIterations = 6

	if _, err := runTestNode(t, node, ctx, wfstate.State{}); err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	if captured == nil {
		t.Fatal("expected a node custom event to be captured")
	}
	checks := map[string]any{
		"kind":                    "session_bootstrap",
		"state_scope":             "agent",
		"input_source":            string(inputSourceConfig),
		"input_chars":             len("do the thing"),
		"has_input":               true,
		"request_input_preserved": false,
		"original_input_set":      true,
		"system_prompt_chars":     len("Be concise."),
		"had_system_prompt":       true,
		"system_prompt_changed":   false,
		"had_existing_messages":   false,
		"message_count":           2,
		"max_iterations":          6,
		"run_id":                  "run-xyz",
	}
	for key, want := range checks {
		if got := captured[key]; got != want {
			t.Fatalf("event[%q] = %#v, want %#v", key, got, want)
		}
	}
	if _, present := captured["unresolved_template_keys"]; present {
		t.Fatalf("expected no unresolved keys, got %#v", captured["unresolved_template_keys"])
	}
}

func TestSessionBootstrapNodeInjectsContextDeadline(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	deadline := fixedNow.Add(45 * time.Second)

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "hello"
	node.NowFunc = func() time.Time { return fixedNow }

	next, err := runTestNode(t, node, ctx, wfstate.State{})
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	budget := next.Get(wfstate.StateKeyBudget)
	if budget == nil {
		t.Fatal("expected budget bucket to be populated")
	}
	if got := budget["deadline"]; got != deadline.Format(time.RFC3339) {
		t.Fatalf("expected deadline %q, got %#v", deadline.Format(time.RFC3339), got)
	}
	if got := budget["remaining_seconds"]; got != 45 {
		t.Fatalf("expected remaining_seconds=45, got %#v", got)
	}
}

func TestSessionBootstrapNodeOmitsBudgetWhenNoDeadline(t *testing.T) {
	t.Parallel()

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "hello"

	next, err := runTestNode(t, node, context.Background(), wfstate.State{})
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	budget := next.Get(wfstate.StateKeyBudget)
	if budget != nil {
		if _, has := budget["deadline"]; has {
			t.Fatalf("expected no deadline without ctx.Deadline, got %#v", budget)
		}
		if _, has := budget["remaining_seconds"]; has {
			t.Fatalf("expected no remaining_seconds without ctx.Deadline, got %#v", budget)
		}
	}
}

func TestSessionBootstrapNodeClampsPastDeadlineToZero(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	deadline := fixedNow.Add(-10 * time.Second)

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	node := NewSessionBootstrapNode()
	node.Input = "hello"
	node.NowFunc = func() time.Time { return fixedNow }

	next, err := runTestNode(t, node, ctx, wfstate.State{})
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	if got := next.Get(wfstate.StateKeyBudget)["remaining_seconds"]; got != 0 {
		t.Fatalf("expected remaining_seconds clamped to 0, got %#v", got)
	}
}

func TestSessionBootstrapNodePreservesExistingBudgetFields(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithDeadline(context.Background(), fixedNow.Add(30*time.Second))
	defer cancel()

	state := wfstate.State{
		wfstate.StateKeyBudget: map[string]any{
			"limits": map[string]any{"max_tokens": 10000},
			"status": "ok",
		},
	}

	node := NewSessionBootstrapNode()
	node.Input = "hello"
	node.NowFunc = func() time.Time { return fixedNow }

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	budget := next.Get(wfstate.StateKeyBudget)
	if got := budget["status"]; got != "ok" {
		t.Fatalf("expected existing status preserved, got %#v", got)
	}
	limits := asStringMap(budget["limits"])
	if limits == nil || limits["max_tokens"] != 10000 {
		t.Fatalf("expected existing limits preserved, got %#v", budget["limits"])
	}
	if budget["remaining_seconds"] != 30 {
		t.Fatalf("expected remaining_seconds=30, got %#v", budget["remaining_seconds"])
	}
}

func TestSessionBootstrapNodeSeedsOriginalInputOnFirstRun(t *testing.T) {
	t.Parallel()

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "first ask"

	next, err := runTestNode(t, node, context.Background(), wfstate.State{})
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	request := next.Get(wfstate.StateKeyRequest)
	if got := request["input"]; got != "first ask" {
		t.Fatalf("expected request.input set, got %#v", got)
	}
	if got := request["original_input"]; got != "first ask" {
		t.Fatalf("expected original_input seeded, got %#v", got)
	}
}

func TestSessionBootstrapNodePreservesOriginalInputAcrossTurns(t *testing.T) {
	t.Parallel()

	state := wfstate.State{
		wfstate.StateKeyRequest: map[string]any{
			"input":          "prior turn",
			"original_input": "the first ask",
		},
	}

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "follow up"

	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	request := next.Get(wfstate.StateKeyRequest)
	if got := request["input"]; got != "follow up" {
		t.Fatalf("expected request.input overwritten with new turn, got %#v", got)
	}
	if got := request["original_input"]; got != "the first ask" {
		t.Fatalf("expected original_input frozen, got %#v", got)
	}
}

func TestSessionBootstrapNodeDoesNotClobberRequestInputOnEmptyResume(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(eventType fruntime.EventType, payload any) error {
		if eventType == fruntime.EventNodeCustom {
			if p, ok := payload.(map[string]any); ok {
				captured = p
			}
		}
		return nil
	})

	state := wfstate.State{
		wfstate.StateKeyRequest: map[string]any{
			"input":          "prior turn",
			"original_input": "the first ask",
		},
	}

	// No Input, no InputPath, no conversation messages — resolveInput will
	// fall through to request.input (rule 3) which returns the prior value;
	// rewriting it is a no-op but request_input_preserved should stay false
	// because we did not skip a write.
	node := NewSessionBootstrapNode()
	node.StateScope = "agent"

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	request := next.Get(wfstate.StateKeyRequest)
	if got := request["input"]; got != "prior turn" {
		t.Fatalf("expected request.input preserved, got %#v", got)
	}
	if got := request["original_input"]; got != "the first ask" {
		t.Fatalf("expected original_input unchanged, got %#v", got)
	}
	if captured["input_source"] != string(inputSourceRequest) {
		t.Fatalf("expected input_source=request_state, got %#v", captured["input_source"])
	}
}

func TestSessionBootstrapNodeReportsPreservedWhenNothingResolved(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(eventType fruntime.EventType, payload any) error {
		if eventType == fruntime.EventNodeCustom {
			if p, ok := payload.(map[string]any); ok {
				captured = p
			}
		}
		return nil
	})

	// original_input was set on a prior bootstrap; this run has neither a
	// new turn (no Input / InputPath / conversation) nor a prior request.input
	// to preserve. We should not re-seed original_input, and there is nothing
	// to "preserve" because nothing was there to begin with.
	state := wfstate.State{
		wfstate.StateKeyRequest: map[string]any{
			"original_input": "the first ask",
			"sentinel":       "stay",
		},
	}

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	request := next.Get(wfstate.StateKeyRequest)
	if got := request["sentinel"]; got != "stay" {
		t.Fatalf("expected sibling fields preserved, got %#v", request)
	}
	if got := request["original_input"]; got != "the first ask" {
		t.Fatalf("expected original_input untouched, got %#v", got)
	}
	if captured["request_input_preserved"] != false {
		t.Fatalf("expected request_input_preserved=false when no prior value, got %#v", captured["request_input_preserved"])
	}
	if captured["original_input_set"] != false {
		t.Fatalf("expected original_input_set=false, got %#v", captured["original_input_set"])
	}
}

func TestSessionBootstrapNodePreservedFlagSetWhenSkippingWriteOverExistingValue(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(eventType fruntime.EventType, payload any) error {
		if eventType == fruntime.EventNodeCustom {
			if p, ok := payload.(map[string]any); ok {
				captured = p
			}
		}
		return nil
	})

	// Configure an input_path that resolves to whitespace-only — trim makes
	// it empty, source is input_path. Pre-existing request.input must be
	// preserved and the flag must be set.
	state := wfstate.State{
		"incoming": map[string]any{"text": "   "},
		wfstate.StateKeyRequest: map[string]any{
			"input":          "prior turn",
			"original_input": "the first ask",
		},
	}

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.InputPath = "incoming.text"

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	request := next.Get(wfstate.StateKeyRequest)
	if got := request["input"]; got != "prior turn" {
		t.Fatalf("expected request.input preserved when new turn resolves to empty, got %#v", got)
	}
	if captured["request_input_preserved"] != true {
		t.Fatalf("expected request_input_preserved=true, got %#v", captured["request_input_preserved"])
	}
	if captured["original_input_set"] != false {
		t.Fatalf("expected original_input_set=false on resume, got %#v", captured["original_input_set"])
	}
}

func TestSessionBootstrapNodeReturnsErrorForMissingExplicitInputPath(t *testing.T) {
	t.Parallel()

	node := NewSessionBootstrapNode()
	node.InputPath = "missing.input"

	_, err := runTestNode(t, node, context.Background(), wfstate.State{})
	if err == nil {
		t.Fatal("expected missing input path error")
	}
}
