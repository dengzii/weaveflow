import { describe, expect, test } from "bun:test";
import type { RunRecord, RuntimeEvent } from "../../types";
import {
  canResumeRun,
  isTerminalRunStatus,
  markRunResuming,
  matchesGraphIdentity,
  missingInitialStateRequirements,
  reconcileRunEvents,
  runControlModeFromRun,
  runStatusFromEvent,
  runTriggerTypesFromInvocations,
  selectedRunIDAfterDeletion,
  upsertRunFromEvent,
} from "./workbenchRunModel";

const baseRun: RunRecord = {
  run_id: "run-1",
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
    expect(isTerminalRunStatus("finished")).toBe(true);
    expect(isTerminalRunStatus("running")).toBe(false);
    expect(runControlModeFromRun(null)).toBe("run");
    expect(runControlModeFromRun({ ...baseRun, status: "running" })).toBe("active");
    expect(runControlModeFromRun({ ...baseRun, status: "paused" })).toBe("run");
    expect(runControlModeFromRun({ ...baseRun, status: "paused", last_checkpoint_id: "checkpoint-1" })).toBe(
      "resume"
    );
    expect(
      canResumeRun(
        { ...baseRun, status: "interrupted" },
        { run_id: "run-1", checkpoint_id: "checkpoint-2" }
      )
    ).toBe(true);
  });

  test("updates known runs and creates unknown runs from events", () => {
    const event: RuntimeEvent = {
      id: "event-1",
      run_id: "run-1",
      type: "run.finished",
      timestamp: "2026-01-01T00:01:00Z",
      payload: { checkpoint_id: "checkpoint-1" },
    };
    expect(upsertRunFromEvent([baseRun], event, "finished", { id: "graph-1", version: "2.0" })[0]).toMatchObject({
      status: "finished",
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

  test("maps Trigger invocations to runs in the current graph", () => {
    expect(
      runTriggerTypesFromInvocations(
        [
          {
            id: "record-1",
            trigger_id: "trigger-1",
            trigger_type: "chat",
            target: { graph_id: "graph-1" },
            status: "finished",
            run: baseRun,
            triggered_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
          {
            id: "record-2",
            trigger_id: "trigger-2",
            trigger_type: "schedule",
            target: { graph_id: "graph-2" },
            status: "finished",
            run: { ...baseRun, run_id: "run-2", graph_id: "graph-2" },
            triggered_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        ],
        "graph-1"
      )
    ).toEqual({ "run-1": "chat" });
  });

  test("marks only the resumed run as running", () => {
    const paused = { ...baseRun, status: "paused", pause_requested: true };
    const other = { ...baseRun, run_id: "run-2", status: "finished" };
    expect(markRunResuming([paused, other], "run-1", "2026-01-01T00:03:00Z")).toEqual([
      { ...paused, status: "running", pause_requested: false, updated_at: "2026-01-01T00:03:00Z" },
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
