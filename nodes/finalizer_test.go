package nodes

import (
	"context"
	"encoding/json"
	"testing"

	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"
)

func TestFinalizerPublishesDirectAnswerEvent(t *testing.T) {
	t.Parallel()

	node := NewFinalizerNode()
	state := wfstate.State{}
	orchestration := state.Ensure(wfstate.StateKeyOrchestration)
	orchestration["mode"] = "direct"
	orchestration["direct_answer"] = "Hi there!"

	var published []fruntime.Event
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(eventType fruntime.EventType, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		published = append(published, fruntime.Event{Type: eventType, NodeID: node.ID(), Payload: data})
		return nil
	})

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	var answerEvent *fruntime.Event
	for i := range published {
		if published[i].Type == fruntime.EventLLMContent {
			answerEvent = &published[i]
			break
		}
	}
	if answerEvent == nil {
		t.Fatal("expected finalizer to publish llm.content event")
	}
	if got := finalizerPayloadText(answerEvent.Payload); got != "Hi there!" {
		t.Fatalf("published answer = %q, want %q", got, "Hi there!")
	}
	if got := next.Conversation(defaultFinalizerScope).FinalAnswer(); got != "Hi there!" {
		t.Fatalf("conversation final answer = %q, want %q", got, "Hi there!")
	}
}

func finalizerPayloadText(payload json.RawMessage) string {
	var mapped map[string]string
	if err := json.Unmarshal(payload, &mapped); err != nil {
		return ""
	}
	return mapped["text"]
}
