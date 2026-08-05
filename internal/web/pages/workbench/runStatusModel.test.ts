import { describe, expect, test } from "bun:test";
import type { CheckpointRecord, RuntimeEvent, StepRecord } from "../../types";
import {
  eventListKey,
  eventMatchesFilters,
  fixedVirtualRange,
  stateHistoryEntries,
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
