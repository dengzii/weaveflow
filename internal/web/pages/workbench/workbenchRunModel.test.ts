import { describe, expect, test } from "bun:test";
import type { RunRecord, RuntimeEvent } from "../../types";
import {
  canResumeRun,
  isTerminalRunStatus,
  markRunResuming,
  matchesGraphIdentity,
  MAX_LIVE_RUNTIME_EVENTS,
  mergeLiveRuntimeEvents,
  mergeRefreshedRuns,
  mergeStoredRuntimeEvents,
  missingInitialStateRequirements,
  partitionLaunchRuntimeEvents,
  reconcileRunEvents,
  runListEventAction,
  runControlModeFromRun,
  runStatusFromEvent,
  runTriggerTypesFromRuns,
  selectedRunIDAfterDeletion,
  shouldRefreshRunInspectionForEvent,
  shouldProjectRuntimeEventToRun,
  runDurationMilliseconds,
  upsertInspectedRun,
  upsertRunFromEvent,
} from "./workbenchRunModel";

const baseRun: RunRecord = {
  run_id: "run-1",
  revision: 1,
  root_run_id: "run-1",
  run_path: ["run-1"],
  namespace: "run-1",
  graph_id: "graph-1",
  graph_version: "2.0",
  status: "running",
  entry_node_id: "start",
  started_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

describe("workbench run model", () => {
  test("maps lifecycle events and derives run controls", () => {
    expect(runStatusFromEvent("run.paused")).toBe("paused");
    expect(runStatusFromEvent("node.finished")).toBe("");
    expect(runStatusFromEvent("run.finished")).toBe("completed");
    expect(isTerminalRunStatus("completed")).toBe(true);
    expect(isTerminalRunStatus("running")).toBe(false);
    expect(runControlModeFromRun(null)).toBe("run");
    expect(runControlModeFromRun({ ...baseRun, status: "running" })).toBe("active");
    expect(runControlModeFromRun({ ...baseRun, status: "paused" })).toBe("run");
    expect(runControlModeFromRun({ ...baseRun, status: "paused", last_checkpoint_id: "checkpoint-1" })).toBe(
      "resume"
    );
    expect(
      canResumeRun(
        { ...baseRun, status: "paused" },
        { run_id: "run-1", checkpoint_id: "checkpoint-2" }
      )
    ).toBe(true);
  });

  test("advances active run durations while preserving paused and terminal durations", () => {
    const now = Date.parse("2026-01-01T00:00:10Z");
    expect(runDurationMilliseconds({ ...baseRun, status: "running", updated_at: "2026-01-01T00:00:02Z" }, now)).toBe(10_000);
    expect(runDurationMilliseconds({ ...baseRun, status: "paused", updated_at: "2026-01-01T00:00:02Z" }, now)).toBe(2_000);
    expect(runDurationMilliseconds({ ...baseRun, status: "completed", finished_at: "2026-01-01T00:00:03Z" }, now)).toBe(3_000);
  });

  test("refreshes selected inspection after pause and terminal lifecycle events", () => {
    expect(shouldRefreshRunInspectionForEvent({ ...runtimeEventWithID("paused"), type: "run.paused" })).toBe(true);
    expect(shouldRefreshRunInspectionForEvent({ ...runtimeEventWithID("finished"), type: "run.finished" })).toBe(true);
    expect(shouldRefreshRunInspectionForEvent({ ...runtimeEventWithID("failed"), type: "run.failed" })).toBe(true);
    expect(shouldRefreshRunInspectionForEvent({ ...runtimeEventWithID("canceled"), type: "run.canceled" })).toBe(true);
    expect(shouldRefreshRunInspectionForEvent(runtimeEventWithID("started"))).toBe(false);
  });

  test("updates known runs and creates unknown runs from events", () => {
    const event: RuntimeEvent = {
      id: "event-1",
      run_id: "run-1",
      type: "run.finished",
      timestamp: "2026-01-01T00:01:00Z",
      payload: { checkpoint_id: "checkpoint-1" },
    };
    expect(upsertRunFromEvent([baseRun], event, "completed", { id: "graph-1", version: "2.0" })[0]).toMatchObject({
      status: "completed",
      last_checkpoint_id: "checkpoint-1",
      finished_at: event.timestamp,
    });

    const created = upsertRunFromEvent([], { ...event, run_id: "run-2", payload: { entry_node_id: "entry" } }, "running", {
      id: "graph-2",
      version: "3.0",
    });
    expect(created[0]).toMatchObject({
      run_id: "run-2",
      graph_id: "graph-2",
      graph_version: "3.0",
      entry_node_id: "entry",
      status: "running",
    });

    const streamingEvent = {
      ...event,
      type: "llm.content_chunk",
      payload: { call_id: "call-1", text: "hello" },
    };
    const existingRuns = [baseRun];
    expect(upsertRunFromEvent(existingRuns, streamingEvent, "", { id: "graph-1", version: "2.0" })).toBe(
      existingRuns
    );
  });

  test("refreshes the run list for unknown lifecycle events", () => {
    const created: RuntimeEvent = {
      id: "event-created",
      run_id: "trigger-run",
      type: "run.created",
      timestamp: "2026-01-01T00:00:01Z",
    };

    expect(runListEventAction([baseRun], created)).toBe("refresh");
    expect(runListEventAction([{ ...baseRun, run_id: "trigger-run" }], created)).toBe("update");
    expect(runListEventAction([baseRun], { ...created, type: "nodes.started" })).toBe("ignore");
    expect(runListEventAction([baseRun], { ...created, run_id: "" })).toBe("ignore");
  });

  test("keeps newer live run state when a list refresh finishes later", () => {
    const finished: RunRecord = {
      ...baseRun,
      status: "completed",
      updated_at: "2026-01-01T00:01:00Z",
      finished_at: "2026-01-01T00:01:00Z",
    };
    const newRun: RunRecord = { ...baseRun, run_id: "run-2" };

    expect(mergeRefreshedRuns([finished], [baseRun, newRun])).toEqual([finished, newRun]);
    expect(mergeRefreshedRuns([finished], [newRun])).toEqual([newRun]);
    expect(mergeRefreshedRuns([finished, newRun], [baseRun], [baseRun])).toEqual([finished, newRun]);
    expect(mergeRefreshedRuns([], [baseRun], [baseRun])).toEqual([]);
  });

  test("does not regress a refreshed run with an older lifecycle event", () => {
    const completed: RunRecord = {
      ...baseRun,
      status: "completed",
      updated_at: "2026-01-01T00:02:00Z",
      finished_at: "2026-01-01T00:02:00Z",
    };
    const current = [completed];
    const staleStarted: RuntimeEvent = {
      id: "event-started",
      run_id: completed.run_id,
      type: "run.started",
      timestamp: "2026-01-01T00:01:00Z",
    };

    expect(upsertRunFromEvent(current, staleStarted, "running", { id: "graph-1", version: "2.0" })).toBe(current);
  });

  test("does not regress a live run with an older inspection response", () => {
    const completed: RunRecord = {
      ...baseRun,
      status: "completed",
      updated_at: "2026-01-01T00:02:00Z",
      finished_at: "2026-01-01T00:02:00Z",
    };
    const current = [completed];

    expect(upsertInspectedRun(current, {
      ...baseRun,
      status: "running",
      updated_at: "2026-01-01T00:01:00Z",
    })).toBe(current);
    expect(upsertInspectedRun(current, {
      ...completed,
      status: "canceled",
      updated_at: "2026-01-01T00:03:00Z",
    })[0]?.status).toBe("canceled");
  });

  test("accepts older authoritative records when lifecycle status matches", () => {
    const projected: RunRecord = {
      ...baseRun,
      status: "paused",
      updated_at: "2026-01-01T00:02:00Z",
    };
    const authoritative: RunRecord = {
      ...projected,
      current_node_id: "review",
      last_checkpoint_id: "checkpoint-1",
      updated_at: "2026-01-01T00:01:00Z",
    };

    expect(upsertInspectedRun([projected], authoritative)).toEqual([authoritative]);
    expect(mergeRefreshedRuns([projected], [authoritative])).toEqual([authoritative]);
  });

  test("batches live events and coalesces LLM chunks by call", () => {
    const first: RuntimeEvent = {
      id: "chunk-1",
      run_id: "run-1",
      step_id: "step-1",
      node_id: "answer",
      type: "llm.content_chunk",
      timestamp: "2026-01-01T00:00:01Z",
      payload: { call_id: "call-1", text: "hel" },
    };
    const second: RuntimeEvent = {
      ...first,
      id: "chunk-2",
      timestamp: "2026-01-01T00:00:02Z",
      payload: { call_id: "call-1", text: "lo" },
    };
    const finished: RuntimeEvent = {
      id: "event-finished",
      run_id: "run-1",
      step_id: "step-1",
      node_id: "answer",
      type: "nodes.finished",
      timestamp: "2026-01-01T00:00:03Z",
    };

    const merged = mergeLiveRuntimeEvents([], [first, second, finished], "run-1");
    expect(merged).toHaveLength(2);
    expect(merged[0]).toBe(finished);
    expect(merged[1]).toMatchObject({
      id: "chunk-1",
      timestamp: second.timestamp,
      payload: { call_id: "call-1", text: "hello" },
    });

    const continued = mergeLiveRuntimeEvents(
      merged,
      [{ ...second, id: "chunk-3", payload: { call_id: "call-1", text: "!" } }],
      "run-1"
    );
    expect(continued).toHaveLength(2);
    expect(continued[0]).toMatchObject({ id: "chunk-1", payload: { text: "hello!" } });

    const replayed = mergeLiveRuntimeEvents(continued, [second], "run-1");
    expect(replayed).toHaveLength(2);
    expect(replayed[0]).toMatchObject({ id: "chunk-1", payload: { text: "hello!" } });
  });

  test("deduplicates ordinary live event ids", () => {
    const event = runtimeEventWithID("duplicate");
    expect(mergeLiveRuntimeEvents([event], [{ ...event }], "run-1")).toEqual([event]);
  });

  test("routes interleaved launch events only by the HTTP run id", () => {
    const otherStarted = { ...runtimeEventWithID("other-started"), run_id: "run-other", type: "run.started" };
    const launchedChunk = {
      ...runtimeEventWithID("launch-chunk"),
      run_id: "run-launched",
      type: "llm.content_chunk",
      payload: { call_id: "call-1", text: "ready" },
    };
    const otherFinished = { ...runtimeEventWithID("other-finished"), run_id: "run-other", type: "run.finished" };
    const launchedFinished = { ...runtimeEventWithID("launch-finished"), run_id: "run-launched", type: "run.finished" };

    expect(partitionLaunchRuntimeEvents(
      [otherStarted, launchedChunk, otherFinished, launchedFinished],
      "run-launched"
    )).toEqual({
      matched: [launchedChunk, launchedFinished],
      unmatched: [otherStarted, otherFinished],
    });
  });

  test("keeps an asynchronous launch response authoritative", async () => {
    const buffered: RuntimeEvent[] = [];
    let resolveRunID: ((runID: string) => void) | undefined;
    const response = new Promise<string>((resolveResponse) => {
      resolveRunID = resolveResponse;
    });
    buffered.push({ ...runtimeEventWithID("other-created"), run_id: "run-other", type: "run.created" });
    await Promise.resolve();
    buffered.push({ ...runtimeEventWithID("launched-finished"), run_id: "run-launched", type: "run.finished" });
    buffered.push({ ...runtimeEventWithID("other-finished"), run_id: "run-other", type: "run.finished" });
    resolveRunID?.("run-launched");

    const runID = await response;
    const routed = partitionLaunchRuntimeEvents(buffered, runID);
    expect(routed.matched.map((event) => event.id)).toEqual(["launched-finished"]);
    expect(routed.unmatched.map((event) => event.id)).toEqual(["other-created", "other-finished"]);
  });

  test("quickly rejects unselected chunks from run projection", () => {
    expect(shouldProjectRuntimeEventToRun({
      ...runtimeEventWithID("chunk"),
      type: "llm.content_chunk",
      payload: { call_id: "call", text: "token" },
    })).toBe(false);
    expect(shouldProjectRuntimeEventToRun({
      ...runtimeEventWithID("checkpoint"),
      payload: { checkpoint_id: "checkpoint-1" },
    })).toBe(true);
  });

  test("caps live events after preserving streaming chunk coalescing", () => {
    const events = Array.from({ length: MAX_LIVE_RUNTIME_EVENTS + 1 }, (_, index): RuntimeEvent => ({
      id: `event-${index}`,
      run_id: "run-1",
      type: "nodes.started",
      timestamp: `2026-01-01T00:00:${String(index % 60).padStart(2, "0")}Z`,
    }));
    const chunk: RuntimeEvent = {
      id: "chunk-1",
      run_id: "run-1",
      step_id: "step-1",
      node_id: "answer",
      type: "llm.content_chunk",
      timestamp: "2026-01-01T00:02:00Z",
      payload: { call_id: "call-1", text: "a" },
    };

    const merged = mergeLiveRuntimeEvents(
      [],
      [...events, chunk, { ...chunk, id: "chunk-2", payload: { call_id: "call-1", text: "b" } }],
      "run-1"
    );

    expect(merged).toHaveLength(MAX_LIVE_RUNTIME_EVENTS);
    expect(merged[0]).toMatchObject({ id: "chunk-1", payload: { text: "ab" } });
  });

  test("appends older stored pages without duplicating event ids", () => {
    const newer = runtimeEventWithID("newer");
    const duplicate = runtimeEventWithID("duplicate");
    const older = runtimeEventWithID("older");

    expect(mergeStoredRuntimeEvents([newer, duplicate], [{ ...duplicate }, older])).toEqual([
      newer,
      duplicate,
      older,
    ]);
  });

  test("matches graph identities and validates typed initial requirements", () => {
    expect(matchesGraphIdentity(baseRun, { id: "graph-1", version: "2.0" })).toBe(true);
    expect(matchesGraphIdentity(baseRun, { id: "graph-1", version: "1.0" })).toBe(false);
    expect(
      missingInitialStateRequirements(
        { shared: { message: "ready", approved: false } },
        [
          { path: "shared.message", type: "string" },
          { path: "shared.approved", type: "boolean" },
          { path: "shared.count", type: "number" },
        ]
      )
    ).toEqual(["shared.count"]);
  });

  test("combines live and stored events for only the selected run", () => {
    const liveEvent: RuntimeEvent = {
      id: "event-live",
      run_id: "run-1",
      type: "node.started",
      node_id: "task",
      timestamp: "2026-01-01T00:02:00Z",
    };
    const duplicate = { ...liveEvent };
    const storedEvent: RuntimeEvent = {
      id: "event-stored",
      run_id: "run-1",
      type: "run.started",
      timestamp: "2026-01-01T00:01:00Z",
    };
    const unrelated = { ...storedEvent, id: "event-other", run_id: "run-2" };

    expect(reconcileRunEvents([liveEvent], [duplicate, storedEvent, unrelated], "run-1")).toEqual([
      liveEvent,
      storedEvent,
    ]);
  });

  test("maps Run origins to trigger types in the current graph", () => {
    expect(
      runTriggerTypesFromRuns(
        [
          { ...baseRun, origin: { type: "chat", trigger_id: "trigger-1" } },
          { ...baseRun, run_id: "run-2", graph_id: "graph-2", origin: { type: "schedule" } },
        ],
        "graph-1"
      )
    ).toEqual({ "run-1": "chat" });
  });

  test("marks only the resumed run as running", () => {
    const paused: RunRecord = { ...baseRun, status: "paused", pause_requested: true };
    const other: RunRecord = { ...baseRun, run_id: "run-2", status: "completed" };
    expect(markRunResuming([paused, other], "run-1")).toEqual([
      { ...paused, status: "running", pause_requested: false },
      other,
    ]);
  });

  test("preserves a valid selection when another run is deleted", () => {
    const runs = [baseRun, { ...baseRun, run_id: "run-2" }, { ...baseRun, run_id: "run-3" }];
    expect(selectedRunIDAfterDeletion(runs, "run-2", "run-1")).toBe("run-1");
    expect(selectedRunIDAfterDeletion(runs, "run-1", "run-1")).toBe("run-3");
    expect(selectedRunIDAfterDeletion([baseRun], "run-1", "run-1")).toBe("");
  });
});

function runtimeEventWithID(id: string): RuntimeEvent {
  return {
    id,
    run_id: "run-1",
    type: "nodes.started",
    timestamp: "2026-01-01T00:00:00Z",
  };
}
