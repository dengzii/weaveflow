package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dengzii/weaveflow/core"
)

func TestAgentInvocationPublishesStableLifecycle(t *testing.T) {
	var payloads []map[string]any
	ctx := WithRunnerEventPublisher(context.Background(), func(eventType EventType, payload any) error {
		if eventType != EventNodeCustom {
			return nil
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		item := map[string]any{}
		if err := json.Unmarshal(data, &item); err != nil {
			return err
		}
		payloads = append(payloads, item)
		return nil
	})
	ctx = WithRunnerMetadata(ctx, RunnerMetadata{RunID: "run", StepID: "step", TaskID: "task", NodeID: "agent", Attempt: 1})
	ctx = core.WithEffectOperation(ctx, core.EffectOperation{Key: "node-operation", Kind: "node"})
	invocationCtx, finish := BeginAgentInvocation(ctx, AgentInvocation{Kind: AgentInvocationModel, Iteration: 2})
	CheckpointAgentInvocation(invocationCtx, "response")
	finish(nil)
	if len(payloads) != 3 {
		t.Fatalf("lifecycle events = %d, want 3", len(payloads))
	}
	first, _ := payloads[0]["agent_invocation"].(map[string]any)
	second, _ := payloads[1]["agent_invocation"].(map[string]any)
	if first["invocation_id"] == "" || first["invocation_id"] != second["invocation_id"] {
		t.Fatalf("invocation IDs = %#v and %#v", first["invocation_id"], second["invocation_id"])
	}
	if payloads[0]["event"] != "agent.invocation.started" || payloads[1]["detail"] != "response" || payloads[2]["event"] != "agent.invocation.finished" {
		t.Fatalf("lifecycle payloads = %#v", payloads)
	}
}
