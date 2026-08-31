import { describe, expect, test } from "bun:test";
import type { CheckpointRecord, RunRecord, RuntimeEvent, StepRecord } from "../../types";
import {
  eventListKey,
  eventMatchesFilters,
  fixedVirtualRange,
  selectRunIOCheckpoints,
  stateHistoryEntries,
  summarizeRunMetrics,
  uniqueSorted,
} from "./runStatusModel";

describe("run status model", () => {
  test("matches all selected event facets and payload keywords", () => {
    const event = runtimeEvent();

    expect(
      eventMatchesFilters(event, {
        mode: "include",
        types: ["nodes.failed"],
        nodes: ["worker"],
        keyword: "timeout",
      })
    ).toBe(true);
    expect(
      eventMatchesFilters(event, {
        mode: "include",
        types: ["nodes.failed"],
        nodes: ["other"],
        keyword: "timeout",
      })
    ).toBe(false);
  });

  test("inverts active criteria in exclude mode without hiding unfiltered events", () => {
    const event = runtimeEvent();

    expect(
      eventMatchesFilters(event, {
        mode: "exclude",
        types: ["nodes.failed"],
        nodes: [],
        keyword: "",
      })
    ).toBe(false);
    expect(
      eventMatchesFilters(event, {
        mode: "exclude",
        types: [],
        nodes: [],
        keyword: "",
      })
    ).toBe(true);
  });

  test("normalizes facet options into a stable unique list", () => {
    expect(uniqueSorted([" warning ", "nodes.failed", "warning", ""])).toEqual([
      "nodes.failed",
      "warning",
    ]);
  });

  test("keeps event ids stable and calculates virtual ranges at the start, middle, and end", () => {
    expect(eventListKey(runtimeEvent(), 4)).toBe("event-event-1");
    expect(eventListKey(runtimeEvent(), 40)).toBe("event-event-1");
    expect(fixedVirtualRange(100, 0, 280, 28, 2)).toEqual({ start: 0, end: 12, offset: 0 });
    expect(fixedVirtualRange(100, 1400, 280, 28, 2)).toEqual({ start: 48, end: 62, offset: 1344 });
    expect(fixedVirtualRange(100, 2600, 280, 28, 2)).toEqual({ start: 90, end: 100, offset: 2520 });
  });

  test("summarizes execution, model, tool, and state metrics for the selected run", () => {
    const run: RunRecord = {
      run_id: "run-1",
      revision: 1,
      root_run_id: "run-1",
      run_path: ["run-1"],
      namespace: "run-1",
      graph_id: "graph",
      graph_version: "1",
      status: "completed",
      entry_node_id: "input",
      started_at: "2026-07-30T02:00:00Z",
      updated_at: "2026-07-30T02:00:05Z",
      finished_at: "2026-07-30T02:00:05Z",
    };
    const steps: StepRecord[] = [
      stepRecord("step-1", "succeeded", 2),
      stepRecord("step-2", "failed", 1),
      { ...stepRecord("other-step", "succeeded", 1), run_id: "other-run" },
    ];
    const checkpoints = [checkpointRecord("checkpoint-1", "after_node", "2026-07-30T02:00:03Z")];
    const events: RuntimeEvent[] = [
      metricEvent("llm.call", { prompt_tokens: 100, completion_tokens: 20, reasoning_tokens: 5, prompt_cached_tokens: 40 }),
      metricEvent("tool.called"),
      metricEvent("tool.failed"),
      metricEvent("state.changed", { changes: [{ path: "shared.a" }, { path: "shared.b" }] }),
      metricEvent("warning"),
      { ...metricEvent("llm.call", { prompt_tokens: 999 }), run_id: "other-run" },
    ];

    expect(summarizeRunMetrics(run, steps, checkpoints, events)).toEqual({
      durationMs: 5_000,
      eventCount: 5,
      stepCount: 2,
      succeededSteps: 1,
      failedSteps: 1,
      activeSteps: 0,
      retries: 1,
      checkpointCount: 1,
      stateChangeCount: 2,
      llmCallCount: 1,
      toolCallCount: 1,
      toolFailureCount: 1,
      promptTokens: 100,
      completionTokens: 20,
      reasoningTokens: 5,
      cachedPromptTokens: 40,
      warningCount: 1,
      errorCount: 1,
    });
  });

  test("uses the supplied clock for active run elapsed time", () => {
    const run: RunRecord = {
      run_id: "run-active",
      revision: 1,
      root_run_id: "run-active",
      run_path: ["run-active"],
      namespace: "run-active",
      graph_id: "graph",
      graph_version: "1",
      status: "running",
      entry_node_id: "input",
      started_at: "2026-07-30T02:00:00Z",
      updated_at: "2026-07-30T02:00:02Z",
    };

    expect(summarizeRunMetrics(run, [], [], [], Date.parse("2026-07-30T02:00:05Z")).durationMs).toBe(5_000);
  });

  test("selects the initial before-node checkpoint and prefers the final output checkpoint", () => {
    const checkpoints = [
      checkpointRecord("checkpoint-after", "after_node", "2026-07-30T02:00:02Z"),
      checkpointRecord("checkpoint-final", "final", "2026-07-30T02:00:04Z"),
      checkpointRecord("checkpoint-before", "before_node", "2026-07-30T02:00:00Z"),
      checkpointRecord("checkpoint-late", "after_node", "2026-07-30T02:00:05Z"),
    ];

    expect(selectRunIOCheckpoints(checkpoints)).toEqual({
      input: checkpoints[2],
      output: checkpoints[1],
    });
    expect(selectRunIOCheckpoints(checkpoints.filter((checkpoint) => checkpoint.stage !== "final"))).toEqual({
      input: checkpoints[2],
      output: checkpoints[3],
    });
  });

  test("builds checkpoint-backed state history with baseline, changes, and parallel barriers", () => {
    const event: RuntimeEvent = {
      id: "state-1",
      run_id: "run-1",
      step_id: "step-1",
      node_id: "worker",
      type: "state.changed",
      timestamp: "2026-07-30T02:00:02Z",
      payload: {
        changes: [
          { path: "shared.answer", after: "ready" },
          { path: "shared.count", before: 1, after: 2 },
          { path: "shared.legacy", before: true },
          "invalid",
        ],
      },
    };

    const steps: StepRecord[] = [{
      step_id: "step-1",
      run_id: "run-1",
      node_id: "worker",
      node_name: "Worker",
      status: "succeeded",
      attempt: 1,
      checkpoint_before_id: "checkpoint-before",
      checkpoint_after_id: "checkpoint-after",
      started_at: "2026-07-30T02:00:00Z",
      updated_at: "2026-07-30T02:00:02Z",
    }];
    const checkpoints: CheckpointRecord[] = [
      checkpointRecord("checkpoint-before", "before_node", "2026-07-30T02:00:00Z"),
      checkpointRecord("checkpoint-after", "after_node", "2026-07-30T02:00:01Z"),
      checkpointRecord("checkpoint-barrier", "after_parallel_wave", "2026-07-30T02:00:03Z"),
    ];

    expect(stateHistoryEntries([runtimeEvent(), event], steps, checkpoints)).toEqual([
      {
        kind: "barrier",
        checkpointID: "checkpoint-barrier",
        checkpoint: checkpoints[2],
        changes: [],
        nodeID: "worker",
        stepID: "step-1",
        timestamp: "2026-07-30T02:00:03Z",
      },
      {
        kind: "change",
        checkpointID: "checkpoint-after",
        checkpoint: checkpoints[1],
        event,
        changes: [
          { path: "shared.answer", kind: "added" },
          { path: "shared.count", kind: "updated" },
          { path: "shared.legacy", kind: "removed" },
          { path: "change 4", kind: "changed" },
        ],
        nodeID: "worker",
        stepID: "step-1",
        timestamp: "2026-07-30T02:00:02Z",
      },
      {
        kind: "baseline",
        checkpointID: "checkpoint-before",
        checkpoint: checkpoints[0],
        changes: [],
        nodeID: "worker",
        stepID: "step-1",
        timestamp: "2026-07-30T02:00:00Z",
      },
    ]);
  });
});

function runtimeEvent(): RuntimeEvent {
  return {
    id: "event-1",
    run_id: "run-1",
    step_id: "step-1",
    node_id: "worker",
    type: "nodes.failed",
    timestamp: "2026-07-30T02:00:01Z",
    payload: { error: "request timeout" },
  };
}

function checkpointRecord(checkpointID: string, stage: string, createdAt: string): CheckpointRecord {
  return {
    checkpoint_id: checkpointID,
    run_id: "run-1",
    step_id: "step-1",
    node_id: "worker",
    stage,
    state_codec: "json",
    state_version: "state-v2",
    created_at: createdAt,
  };
}

function stepRecord(stepID: string, status: StepRecord["status"], attempt: number): StepRecord {
  return {
    step_id: stepID,
    run_id: "run-1",
    task_id: `${stepID}-task`,
    node_id: "worker",
    node_name: "Worker",
    status,
    attempt,
    started_at: "2026-07-30T02:00:00Z",
    updated_at: "2026-07-30T02:00:02Z",
  };
}

function metricEvent(type: string, payload?: unknown): RuntimeEvent {
  return {
    id: `event-${type}`,
    run_id: "run-1",
    type,
    timestamp: "2026-07-30T02:00:02Z",
    payload,
  };
}
