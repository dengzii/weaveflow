package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEventAnalyzerSummarizesCompleteRunActivity(t *testing.T) {
	startedAt := time.Date(2026, 8, 26, 2, 0, 0, 0, time.UTC)
	event := func(offset int, eventType EventType, nodeID, stepID, payload string) Event {
		return Event{
			ID:        string(eventType) + "-event",
			RunID:     "run-1",
			NodeID:    nodeID,
			StepID:    stepID,
			Type:      eventType,
			Timestamp: startedAt.Add(time.Duration(offset) * time.Second),
			Payload:   json.RawMessage(payload),
		}
	}
	events := []Event{
		event(22, EventRunFinished, "", "", `{}`),
		event(0, EventRunCreated, "", "", `{"entry_node_id":"planner"}`),
		event(1, EventRunStarted, "", "", `{"node_id":"planner"}`),
		event(2, EventNodeStarted, "planner", "step-1", `{"node_name":"Planner"}`),
		event(3, EventNodeRetry, "planner", "step-1", `{}`),
		event(4, EventLLMUsage, "planner", "step-1", `{"calls":2,"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"reasoning_tokens":3,"prompt_cached_tokens":4,"model":"gpt-test","cost_currency":"usd","cost_total":0.25}`),
		event(5, EventLLMReasoningChunk, "planner", "step-1", `{"text":"think"}`),
		event(6, EventLLMContent, "planner", "step-1", `{"text":"answer"}`),
		event(7, EventToolCalled, "planner", "step-1", `{"tools":[{"tool_call_id":"call-1","name":"search"},{"tool_call_id":"call-2","name":"calculator"}]}`),
		event(8, EventToolReturned, "planner", "step-1", `{"tool_call_id":"call-1","name":"search"}`),
		event(9, EventToolFailed, "planner", "step-1", `{"tool_call_id":"call-2","name":"calculator","error_code":"tool_error","error":"bad expression"}`),
		event(10, EventToolReturned, "planner", "step-1", `{"tool_call_id":"unmatched","name":"cache"}`),
		event(11, EventSubgraphStarted, "planner", "step-1", `{"graph_ref":"review.v1"}`),
		event(12, EventSubgraphFinished, "planner", "step-1", `{"graph_ref":"review.v1"}`),
		event(13, EventSubgraphFailed, "planner", "step-1", `{"graph_ref":"fallback.v1","error":"subgraph failed"}`),
		event(14, EventStateChanged, "planner", "step-1", `{"changes":[{"path":"shared.a"},{"path":"shared.b"}]}`),
		event(15, EventCheckpointCreated, "planner", "step-1", `{"checkpoint_id":"checkpoint-1","stage":"after_node"}`),
		event(16, EventArtifactCreated, "planner", "step-1", `{"artifact_id":"artifact-1","type":"report","mime_type":"text/plain"}`),
		event(17, EventWarning, "planner", "step-1", `{"message":"slow"}`),
		event(18, EventContractViolation, "planner", "step-1", `{"violations":[{"path":"shared.x"},{"path":"shared.y"}]}`),
		event(19, EventNodeFinished, "planner", "step-1", `{"attempt":2}`),
		event(20, EventNodeStarted, "worker", "step-2", `{"node_name":"Worker"}`),
		event(21, EventNodeFailed, "worker", "step-2", `{"attempt":1,"error_code":"node_error","error":"worker failed"}`),
	}

	analysis := AnalyzeRunEvents("", events)
	if analysis.RunID != "run-1" || analysis.Status != RunStatusCompleted || analysis.EntryNodeID != "planner" || analysis.CurrentNodeID != "worker" {
		t.Fatalf("run analysis identity = %#v", analysis)
	}
	if analysis.EventCount != len(events) || analysis.Duration != 22*time.Second || !analysis.StartedAt.Equal(startedAt) || !analysis.FinishedAt.Equal(startedAt.Add(22*time.Second)) {
		t.Fatalf("run timing = %#v", analysis)
	}
	if len(analysis.Timeline) != len(events) || analysis.Timeline[0].Type != EventRunCreated || analysis.Timeline[len(analysis.Timeline)-1].Type != EventRunFinished {
		t.Fatalf("timeline = %#v", analysis.Timeline)
	}
	if analysis.LLM.Calls != 2 || analysis.LLM.TotalTokens != 15 || analysis.LLM.ReasoningChars != 5 || analysis.LLM.ContentChars != 6 || analysis.LLM.CostByCurrency["USD"] != 0.25 || analysis.LLM.ByModel["gpt-test"].PromptTokens != 10 {
		t.Fatalf("LLM usage = %#v", analysis.LLM)
	}
	if analysis.Tools.Called != 2 || analysis.Tools.Returned != 2 || analysis.Tools.Failed != 1 || len(analysis.Tools.Calls) != 3 || analysis.Tools.ByName["search"].Duration != time.Second {
		t.Fatalf("tool usage = %#v", analysis.Tools)
	}
	if analysis.Subgraphs.Started != 1 || analysis.Subgraphs.Finished != 1 || analysis.Subgraphs.Failed != 1 || len(analysis.Subgraphs.Calls) != 2 || analysis.Subgraphs.ByRef["review.v1"].Duration != time.Second {
		t.Fatalf("subgraph usage = %#v", analysis.Subgraphs)
	}
	if analysis.State.ChangeEvents != 1 || analysis.State.ChangeCount != 2 || analysis.Checkpoints.Created != 1 || analysis.Checkpoints.Items[0].CheckpointID != "checkpoint-1" || analysis.Artifacts.Created != 1 || analysis.Artifacts.Items[0].ArtifactID != "artifact-1" {
		t.Fatalf("state/checkpoint/artifact usage = %#v %#v %#v", analysis.State, analysis.Checkpoints, analysis.Artifacts)
	}
	if len(analysis.Nodes) != 2 || analysis.Nodes[0].NodeID != "planner" || analysis.Nodes[0].RetryCount != 1 || analysis.Nodes[0].AttemptCount != 2 || analysis.Nodes[0].Duration != 17*time.Second || analysis.Nodes[0].ContractViolationCount != 2 || analysis.Nodes[1].NodeID != "worker" || analysis.Nodes[1].Failed != 1 {
		t.Fatalf("node usage = %#v", analysis.Nodes)
	}
	if len(analysis.Warnings) != 1 || len(analysis.ContractViolations) != 1 || len(analysis.Errors) != 3 {
		t.Fatalf("diagnostics = warnings %#v violations %#v errors %#v", analysis.Warnings, analysis.ContractViolations, analysis.Errors)
	}
}

func TestEventAnalyzerStoragePaginationExportAndReset(t *testing.T) {
	analyzer := NewEventAnalyzer()
	payload := json.RawMessage(`{"node_id":"entry"}`)
	if err := analyzer.Publish(context.Background(), Event{ID: "b-1", RunID: "run-b", Type: EventRunStarted, Timestamp: time.Unix(1, 0), Payload: payload}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	batch := []Event{
		{ID: "a-1", RunID: "run-a", Type: EventRunStarted, Timestamp: time.Unix(2, 0)},
		{ID: "a-2", RunID: "run-a", Type: EventRunFinished, Timestamp: time.Unix(3, 0)},
	}
	if err := analyzer.PublishBatch(context.Background(), batch); err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}
	copy(payload, []byte(`{"node_id":"other"}`))
	batch[0].ID = "mutated"

	if got := analyzer.RunIDs(); len(got) != 2 || got[0] != "run-a" || got[1] != "run-b" {
		t.Fatalf("RunIDs() = %#v", got)
	}
	stored, err := analyzer.ListEvents("run-a")
	if err != nil || len(stored) != 2 || stored[0].ID != "a-1" {
		t.Fatalf("ListEvents() = %#v, %v", stored, err)
	}
	stored[0].ID = "changed"
	storedAgain, _ := analyzer.ListEvents("run-a")
	if storedAgain[0].ID != "a-1" {
		t.Fatalf("ListEvents() returned aliased events: %#v", storedAgain)
	}
	page, err := analyzer.ListEventPage("run-a", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "a-2" || page.NextCursor != "1" {
		t.Fatalf("ListEventPage() = %#v, %v", page, err)
	}
	analysis, err := analyzer.AnalyzeRun("run-a")
	if err != nil || analysis.Status != RunStatusCompleted {
		t.Fatalf("AnalyzeRun() = %#v, %v", analysis, err)
	}
	analyses, err := analyzer.AnalyzeRuns()
	if err != nil || len(analyses) != 2 || analyses[0].RunID != "run-a" {
		t.Fatalf("AnalyzeRuns() = %#v, %v", analyses, err)
	}
	if data, err := analyzer.ExportRunJSON("run-a"); err != nil || !strings.Contains(string(data), `"run_id": "run-a"`) {
		t.Fatalf("ExportRunJSON() = %s, %v", data, err)
	}
	if data, err := analyzer.ExportRunsJSON(); err != nil || !strings.Contains(string(data), `"run_id": "run-b"`) {
		t.Fatalf("ExportRunsJSON() = %s, %v", data, err)
	}

	analyzer.Reset()
	if len(analyzer.RunIDs()) != 0 {
		t.Fatalf("Reset() retained runs: %#v", analyzer.RunIDs())
	}
	var nilAnalyzer *EventAnalyzer
	if err := nilAnalyzer.Publish(context.Background(), Event{}); err != nil {
		t.Fatalf("nil Publish() error = %v", err)
	}
	if events, err := nilAnalyzer.ListEvents("missing"); err != nil || len(events) != 0 || nilAnalyzer.RunIDs() != nil {
		t.Fatalf("nil analyzer methods = %#v, %v, %#v", events, err, nilAnalyzer.RunIDs())
	}
	nilAnalyzer.Reset()
}

func TestAnalyzeRunEventsHandlesEmptyAndTerminalFailures(t *testing.T) {
	if analysis := AnalyzeRunEvents("explicit", nil); analysis.RunID != "explicit" || analysis.Status != "" || analysis.EventCount != 0 || analysis.EventCounts != nil {
		t.Fatalf("empty analysis = %#v", analysis)
	}
	failedAt := time.Date(2026, 8, 26, 3, 0, 0, 0, time.UTC)
	failed := AnalyzeRunEvents("failed", []Event{{
		RunID: "failed", Type: EventRunFailed, Timestamp: failedAt,
		Payload: json.RawMessage(`{"error_code":"fatal","error_message":"run failed"}`),
	}})
	if failed.Status != RunStatusFailed || len(failed.Errors) != 1 || failed.Errors[0].Code != "fatal" || failed.Errors[0].Message != "run failed" {
		t.Fatalf("failed analysis = %#v", failed)
	}
	canceled := AnalyzeRunEvents("canceled", []Event{{
		RunID: "canceled", Type: EventRunCanceled, Timestamp: failedAt,
		Payload: json.RawMessage(`{"error":"user canceled"}`),
	}})
	if canceled.Status != RunStatusCanceled || len(canceled.Errors) != 1 || canceled.Errors[0].Message != "user canceled" {
		t.Fatalf("canceled analysis = %#v", canceled)
	}
}
