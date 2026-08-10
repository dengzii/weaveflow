package utilities

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

func TestAnalyzeGraphRunnerAggregatesRunStats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(base + "/execution")
	checkpointStore := fruntime.NewFileCheckpointStore(base + "/checkpoints")
	artifactStore := fruntime.NewFileArtifactStore(base + "/artifacts")
	eventSink := fruntime.NewFileEventSink(base + "/events")

	startedAt := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	nodeFinishedAt := startedAt.Add(3 * time.Second)
	finishedAt := startedAt.Add(5 * time.Second)
	run := fruntime.RunRecord{
		RunID:            "run-1",
		GraphID:          "graph-1",
		GraphVersion:     "v1",
		Status:           fruntime.RunStatusCompleted,
		EntryNodeID:      "planner",
		CurrentNodeID:    "planner",
		LastStepID:       "step-1",
		LastCheckpointID: "checkpoint-1",
		StartedAt:        startedAt,
		UpdatedAt:        finishedAt,
		FinishedAt:       &finishedAt,
	}
	if err := executionStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := executionStore.AppendStep(ctx, fruntime.StepRecord{
		StepID:            "step-1",
		RunID:             run.RunID,
		NodeID:            "planner",
		NodeName:          "Planner",
		Attempt:           2,
		Status:            fruntime.StepStatusSucceeded,
		CheckpointAfterID: "checkpoint-1",
		StartedAt:         startedAt.Add(time.Second),
		UpdatedAt:         nodeFinishedAt,
		FinishedAt:        &nodeFinishedAt,
	}); err != nil {
		t.Fatalf("append step: %v", err)
	}
	if err := checkpointStore.Save(ctx, fruntime.CheckpointRecord{
		CheckpointID: "checkpoint-1",
		RunID:        run.RunID,
		StepID:       "step-1",
		NodeID:       "planner",
		Stage:        fruntime.CheckpointAfterNode,
		StateCodec:   "json",
		CreatedAt:    nodeFinishedAt,
	}, []byte(`{}`)); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if _, err := artifactStore.Save(ctx, fruntime.Artifact{
		ID:        "artifact-1",
		RunID:     run.RunID,
		StepID:    "step-1",
		NodeID:    "planner",
		Type:      "tool.output",
		Data:      []byte("ok"),
		CreatedAt: nodeFinishedAt,
	}); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	publish := func(event fruntime.Event) {
		t.Helper()
		event.RunID = run.RunID
		event.StepID = "step-1"
		event.NodeID = "planner"
		if event.Timestamp.IsZero() {
			event.Timestamp = startedAt
		}
		if err := eventSink.Publish(ctx, event); err != nil {
			t.Fatalf("publish %s: %v", event.Type, err)
		}
	}
	publish(eventWithPayload(t, fruntime.EventNodeRetry, map[string]any{"attempt": 1}))
	publish(eventWithPayload(t, fruntime.EventLLMCall, map[string]any{
		"model":                "model-a",
		"calls":                1,
		"prompt_tokens":        11,
		"completion_tokens":    7,
		"total_tokens":         18,
		"reasoning_tokens":     2,
		"prompt_cached_tokens": 3,
	}))
	publish(eventWithPayload(t, fruntime.EventToolCalled, map[string]any{
		"tools": []map[string]any{
			{"name": "read"},
			{"name": "write"},
		},
		"count": 2,
	}))
	publish(eventWithPayload(t, fruntime.EventToolReturned, map[string]any{"name": "read"}))
	publish(eventWithPayload(t, fruntime.EventToolFailed, map[string]any{"name": "write", "error": "boom"}))
	publish(eventWithPayload(t, fruntime.EventSubgraphStarted, map[string]any{"graph_ref": "child"}))
	publish(eventWithPayload(t, fruntime.EventSubgraphFinished, map[string]any{"graph_ref": "child"}))
	publish(eventWithPayload(t, fruntime.EventStateChanged, map[string]any{
		"changes": []map[string]any{{"path": "a"}, {"path": "b"}},
	}))

	analyzer := NewAnalyzeGraphRunnerFromStores(executionStore, checkpointStore, artifactStore, eventSink)
	analysis, err := analyzer.AnalyzeRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("analyze run: %v", err)
	}

	if !analysis.State.Completed || !analysis.State.Terminal || analysis.State.Active {
		t.Fatalf("unexpected state flags: %+v", analysis.State)
	}
	if analysis.State.Duration != 5*time.Second {
		t.Fatalf("duration = %s, want 5s", analysis.State.Duration)
	}
	if analysis.Stats.StepCount != 1 || analysis.Stats.AttemptCount != 2 {
		t.Fatalf("step stats = %+v", analysis.Stats)
	}
	if analysis.Stats.CheckpointCount != 1 || analysis.Stats.ArtifactCount != 1 {
		t.Fatalf("checkpoint/artifact stats = %+v", analysis.Stats)
	}
	if analysis.Stats.LLM.Calls != 1 || analysis.Stats.LLM.TotalTokens != 18 {
		t.Fatalf("llm stats = %+v", analysis.Stats.LLM)
	}
	if analysis.Stats.LLM.Models["model-a"].PromptCachedTokens != 3 {
		t.Fatalf("model stats = %+v", analysis.Stats.LLM.Models)
	}
	if analysis.Stats.Tools.Called != 2 || analysis.Stats.Tools.Returned != 1 || analysis.Stats.Tools.Failed != 1 {
		t.Fatalf("tool stats = %+v", analysis.Stats.Tools)
	}
	if analysis.Stats.Tools.ByName["write"].Failed != 1 {
		t.Fatalf("tool name stats = %+v", analysis.Stats.Tools.ByName)
	}
	if analysis.Stats.Subgraphs.Started != 1 || analysis.Stats.Subgraphs.Finished != 1 {
		t.Fatalf("subgraph stats = %+v", analysis.Stats.Subgraphs)
	}
	if analysis.Stats.StateChangeCount != 2 {
		t.Fatalf("state changes = %d, want 2", analysis.Stats.StateChangeCount)
	}
	if len(analysis.NodeStats) != 1 {
		t.Fatalf("node stats length = %d, want 1", len(analysis.NodeStats))
	}
	nodeStats := analysis.NodeStats[0]
	if nodeStats.NodeID != "planner" || nodeStats.NodeName != "Planner" || nodeStats.RetryCount != 1 || nodeStats.ToolCalls != 2 || nodeStats.LLMCalls != 1 {
		t.Fatalf("node stats = %+v", nodeStats)
	}
}

func TestAnalyzeLatestRunUsesMostRecentlyUpdatedRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	executionStore := fruntime.NewFileExecutionStore(t.TempDir())
	oldUpdatedAt := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	newUpdatedAt := oldUpdatedAt.Add(time.Hour)

	for _, run := range []fruntime.RunRecord{
		{RunID: "old", Status: fruntime.RunStatusCompleted, StartedAt: oldUpdatedAt, UpdatedAt: oldUpdatedAt},
		{RunID: "new", Status: fruntime.RunStatusRunning, StartedAt: oldUpdatedAt, UpdatedAt: newUpdatedAt},
	} {
		if err := executionStore.CreateRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.RunID, err)
		}
	}

	analyzer := NewAnalyzeGraphRunnerFromStores(executionStore, nil, nil, nil)
	analysis, err := analyzer.AnalyzeLatestRun(ctx, fruntime.RunFilter{})
	if err != nil {
		t.Fatalf("analyze latest run: %v", err)
	}
	if analysis == nil || analysis.Run.RunID != "new" {
		t.Fatalf("latest run = %#v, want new", analysis)
	}
	if len(analysis.Warnings) != 1 {
		t.Fatalf("warnings = %#v, want event reader warning", analysis.Warnings)
	}
}

func TestAnalyzeGraphRunnerCountsCanceledSteps(t *testing.T) {
	t.Parallel()

	steps := []fruntime.StepRecord{{NodeID: "work", Status: fruntime.StepStatusCanceled, Attempt: 1}}
	stats := buildGraphRunStats(steps, 0, 0, nil, nil)
	nodes := buildNodeRunStats(steps, nil)
	if stats.CanceledStepCount != 1 || stats.FailedStepCount != 0 {
		t.Fatalf("run stats = %#v", stats)
	}
	if len(nodes) != 1 || nodes[0].Canceled != 1 || nodes[0].Failed != 0 {
		t.Fatalf("node stats = %#v", nodes)
	}
}

func eventWithPayload(t *testing.T, eventType fruntime.EventType, payload any) fruntime.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return fruntime.Event{
		Type:    eventType,
		Payload: raw,
	}
}
