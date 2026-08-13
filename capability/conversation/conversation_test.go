package conversation

import (
	"encoding/json"
	"testing"

	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

func TestViewBindsArbitraryRootsAndKeepsConversationsIsolated(t *testing.T) {
	t.Parallel()
	access := state.NewEditingAccess(state.NewState())
	sharedRoot := state.Shared("threads", "primary")
	scopedRoot := state.Scope("researcher", "private_thread")
	shared, err := Bind(access, sharedRoot)
	if err != nil {
		t.Fatalf("bind shared root: %v", err)
	}
	scoped, err := Bind(access, scopedRoot)
	if err != nil {
		t.Fatalf("bind scoped root: %v", err)
	}

	if err := shared.AppendMessage(llms.TextParts(llms.ChatMessageTypeHuman, "shared question")); err != nil {
		t.Fatalf("append shared message: %v", err)
	}
	if err := shared.SetFinalAnswer("shared answer"); err != nil {
		t.Fatalf("set shared answer: %v", err)
	}
	if err := scoped.AppendMessage(llms.TextParts(llms.ChatMessageTypeHuman, "private question")); err != nil {
		t.Fatalf("append scoped message: %v", err)
	}
	if err := scoped.SetFinalAnswer("private answer"); err != nil {
		t.Fatalf("set scoped answer: %v", err)
	}

	if shared.Path().String() != "shared.threads.primary" || scoped.Path().String() != "scopes.researcher.private_thread" {
		t.Fatalf("unexpected roots shared=%q scoped=%q", shared.Path().String(), scoped.Path().String())
	}
	if got := messageText(shared.Messages()[0]); got != "shared question" {
		t.Fatalf("shared message = %q", got)
	}
	if got := messageText(scoped.Messages()[0]); got != "private question" {
		t.Fatalf("scoped message = %q", got)
	}
	if shared.FinalAnswer() != "shared answer" || scoped.FinalAnswer() != "private answer" {
		t.Fatalf("answers leaked shared=%q scoped=%q", shared.FinalAnswer(), scoped.FinalAnswer())
	}
}

func TestDefinitionFieldsMatchViewFields(t *testing.T) {
	t.Parallel()
	want := []string{FieldMessages, FieldFinalAnswer, FieldIterationCount, FieldMaxIterations}
	definition := Definition()
	if definition.ID != CapabilityID {
		t.Fatalf("capability id = %q", definition.ID)
	}
	if len(definition.Fields) != len(want) {
		t.Fatalf("definition fields = %#v", definition.Fields)
	}
	for index, name := range want {
		if definition.Fields[index].Name != name {
			t.Fatalf("definition field %d = %q, want %q", index, definition.Fields[index].Name, name)
		}
	}
}

func TestViewDecodesMessagesAfterJSONSnapshotRoundTripAtArbitraryRoot(t *testing.T) {
	t.Parallel()
	root := state.Scope("researcher", "thread")
	access := state.NewEditingAccess(state.NewState())
	view, err := Bind(access, root)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "find facts"),
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{llms.ToolCall{
				ID: "call-1", Type: "function",
				FunctionCall: &llms.FunctionCall{Name: "search", Arguments: json.RawMessage(`{"q":"facts"}`)},
			}},
		},
	}
	if err := view.SetMessages(messages); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	if err := view.SetIterationCount(3); err != nil {
		t.Fatalf("set iteration: %v", err)
	}

	restored := roundTripState(t, access.State())
	restoredView, err := Bind(state.NewAccess(restored), root)
	if err != nil {
		t.Fatalf("bind restored: %v", err)
	}
	got, err := restoredView.ReadMessages()
	if err != nil {
		t.Fatalf("read restored messages: %v", err)
	}
	if len(got) != 2 || got[0].Role != llms.ChatMessageTypeHuman || messageText(got[0]) != "find facts" {
		t.Fatalf("restored messages = %#v", got)
	}
	toolCall, ok := got[1].Parts[0].(llms.ToolCall)
	if !ok || toolCall.FunctionCall == nil || toolCall.FunctionCall.Name != "search" {
		t.Fatalf("restored tool call = %#v", got[1].Parts)
	}
	if restoredView.IterationCount() != 3 {
		t.Fatalf("restored iteration = %d", restoredView.IterationCount())
	}
}

func roundTripState(t *testing.T, input *state.State) *state.State {
	t.Helper()
	snapshot, err := state.SnapshotFromState(input)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	codec := state.NewJSONStateCodec("")
	payload, err := codec.Encode(snapshot)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := codec.Decode(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	restored, err := state.FromSnapshot(decoded)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	return restored
}

func messageText(message llms.MessageContent) string {
	for _, part := range message.Parts {
		if text, ok := part.(llms.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
