import { describe, expect, test } from "bun:test";
import type { CheckpointRecord, RunInspection, RuntimeEvent } from "../../types";
import {
  checkpointRecordFromEvent,
  mergeFetchedCheckpoints,
  rememberRunInspection,
  upsertCheckpoint,
} from "./runInspectionModel";

describe("run inspection model", () => {
  test("builds checkpoint placeholders only from valid checkpoint events", () => {
    const event: RuntimeEvent = {
      id: "event-1",
      run_id: "run-1",
      step_id: "step-1",
      node_id: "node-1",
      type: "checkpoint.created",
      timestamp: "2026-08-09T00:00:00Z",
      payload: { checkpoint_id: " checkpoint-1 ", stage: " after " },
    };

    expect(checkpointRecordFromEvent(event)).toEqual({
      checkpoint_id: "checkpoint-1",
      run_id: "run-1",
      step_id: "step-1",
      node_id: "node-1",
      stage: "after",
      state_codec: "",
      state_version: "",
      created_at: event.timestamp,
    });
    expect(checkpointRecordFromEvent({ ...event, type: "node.finished" })).toBeNull();
    expect(checkpointRecordFromEvent({ ...event, payload: { checkpoint_id: "checkpoint-1" } })).toBeNull();
  });

  test("merges event placeholders and fetched checkpoint details deterministically", () => {
    const full = checkpoint("checkpoint-1", "2026-08-09T00:01:00Z");
    const placeholder = { ...full, state_codec: "", state_version: "" };
    const older = checkpoint("checkpoint-0", "2026-08-09T00:00:00Z");

    expect(upsertCheckpoint([full], placeholder)).toEqual([full]);
    expect(upsertCheckpoint([full], older)).toEqual([older, full]);
    expect(mergeFetchedCheckpoints([placeholder], [full])).toEqual([full]);
  });

  test("refreshes inspection recency and evicts the oldest entry", () => {
    const first = inspection("run-1");
    const second = inspection("run-2");
    const refreshedFirst = inspection("run-1", "completed");

    let cache = rememberRunInspection(new Map(), first, 2);
    cache = rememberRunInspection(cache, second, 2);
    cache = rememberRunInspection(cache, refreshedFirst, 2);
    cache = rememberRunInspection(cache, inspection("run-3"), 2);

    expect([...cache.keys()]).toEqual(["run-1", "run-3"]);
    expect(cache.get("run-1")?.run.status).toBe("completed");
  });
});

function checkpoint(checkpointID: string, createdAt: string): CheckpointRecord {
  return {
    checkpoint_id: checkpointID,
    run_id: "run-1",
    step_id: "step-1",
    node_id: "node-1",
    stage: "after",
    state_codec: "json",
    state_version: "1",
    created_at: createdAt,
  };
}

function inspection(runID: string, status: "running" | "completed" = "running"): RunInspection {
  return {
    run: {
      run_id: runID,
      graph_id: "graph-1",
      graph_version: "2.0",
      status,
      entry_node_id: "start",
      started_at: "2026-08-09T00:00:00Z",
      updated_at: "2026-08-09T00:00:00Z",
    },
    steps: [],
    checkpoints: [],
    events: { items: [], next_cursor: "" },
  };
}
