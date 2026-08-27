import { describe, expect, test } from "bun:test";
import type { Node } from "@xyflow/react";
import type { GraphDefinition, RuntimeEvent, StepRecord } from "../types";
import { END_NODE_REF, START_NODE_REF } from "../lib/graphEditor";
import {
  applyRuntime,
  applyRuntimeStep,
  applyRuntimeSnapshot,
  eventAttempt,
  eventAttemptStartedAt,
  eventErrorMessage,
  formatRuntimeDuration,
  isVirtualEndNodeID,
  isVirtualStartNodeID,
  layoutNodes,
  runtimeFromExecution,
  runtimeFromSteps,
  runtimeDurations,
  runtimeStatusFromEvent,
  virtualNodeSpec,
  type FlowNodeData,
  type RuntimeNodeState,
} from "./graphCanvasModel";

function step(overrides: Partial<StepRecord>): StepRecord {
  return {
    step_id: "step-1",
    run_id: "run-1",
    node_id: "node-1",
    node_name: "Node 1",
    status: "running",
    attempt: 1,
    started_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function flowNode(id: string, data?: Partial<FlowNodeData>): Node<FlowNodeData> {
  return {
    id,
    position: { x: 0, y: 0 },
    data: {
      label: id,
      type: "task",
      status: "idle",
      editable: true,
      ...data,
    },
  };
}

function runtimeEvent(overrides: Partial<RuntimeEvent>): RuntimeEvent {
  return {
    id: "event-1",
    run_id: "run-1",
    step_id: "step-1",
    node_id: "node-1",
    type: "nodes.started",
    timestamp: new Date(1_000).toISOString(),
    ...overrides,
  };
}

describe("graph canvas model", () => {
  test("merges step snapshots by run, timestamp, and highest attempt", () => {
    const runtime = runtimeFromSteps(
      [
        step({ status: "succeeded", attempt: 1, updated_at: "2026-01-01T00:02:00Z" }),
        step({ status: "running", attempt: 2, updated_at: "2026-01-01T00:01:00Z" }),
        step({ run_id: "run-2", status: "failed", attempt: 3 }),
      ],
      "run-1"
    );
    expect(runtime.get("node-1")).toEqual({
      status: "succeeded",
      executionCount: 2,
      at: Date.parse("2026-01-01T00:02:00Z"),
      stepAttempts: new Map([["step-1", 2]]),
      stepTimings: new Map([[
        "step:step-1",
        {
          stepID: "step-1",
          startedAt: Date.parse("2026-01-01T00:00:00Z"),
          finishedAt: Date.parse("2026-01-01T00:02:00Z"),
        },
      ]]),
    });
  });

  test("totals attempts across repeated executions of the same node", () => {
    const runtime = runtimeFromSteps([
      step({ step_id: "step-1", status: "succeeded", attempt: 1, updated_at: "2026-01-01T00:01:00Z" }),
      step({ step_id: "step-2", status: "succeeded", attempt: 2, updated_at: "2026-01-01T00:02:00Z" }),
      step({ step_id: "step-3", status: "running", attempt: 1, updated_at: "2026-01-01T00:03:00Z" }),
    ]);
    expect(runtime.get("node-1")).toMatchObject({
      status: "running",
      executionCount: 4,
    });
  });

  test("maps only declared step statuses", () => {
    expect(runtimeFromSteps([step({ status: "scheduled" })]).get("node-1")?.status).toBe("idle");
    expect(runtimeFromSteps([step({ status: "pending" as StepRecord["status"] })]).get("node-1")?.status).toBe("idle");
    expect(runtimeFromSteps([step({ status: "running" })]).get("node-1")?.status).toBe("running");
    expect(runtimeFromSteps([step({ status: "canceled" })]).get("node-1")?.status).toBe("canceled");
  });

  test("retains the latest node failure summary and reads event errors", () => {
    const runtime = runtimeFromSteps([
      step({ status: "failed", error_message: "provider request timed out" }),
    ]);
    expect(runtime.get("node-1")?.errorMessage).toBe("provider request timed out");
    expect(eventErrorMessage({ error_message: " model unavailable " })).toBe("model unavailable");
    expect(eventErrorMessage({ error: "fallback error" })).toBe("fallback error");
  });

  test("ignores stale runtime status while retaining a newer attempt", () => {
    const runtime = new Map<string, RuntimeNodeState>([
      ["node-1", {
        status: "succeeded",
        executionCount: 1,
        at: 20,
        stepAttempts: new Map([["step-1", 1]]),
      }],
    ]);
    expect(applyRuntimeStep(runtime, "node-1", "step-1", "running", 2, 10)).toBe(true);
    expect(runtime.get("node-1")).toEqual({
      status: "succeeded",
      executionCount: 2,
      at: 20,
      stepAttempts: new Map([["step-1", 2]]),
    });
    expect(applyRuntimeStep(runtime, "node-1", "step-1", "running", 2, 10)).toBe(false);
  });

  test("merges step attempts without double counting snapshots", () => {
    const runtime = runtimeFromSteps([
      step({ step_id: "step-1", attempt: 1 }),
    ]);
    expect(applyRuntime(runtime, "node-1", runtimeFromSteps([
      step({ step_id: "step-1", attempt: 1 }),
      step({ step_id: "step-2", attempt: 2 }),
    ]).get("node-1")!)).toBe(true);
    expect(runtime.get("node-1")?.executionCount).toBe(3);
  });

  test("accumulates live executions by step without replay duplicates", () => {
    const runtime = new Map<string, RuntimeNodeState>();
    expect(applyRuntimeStep(runtime, "node-1", "step-1", "running", 1, 1)).toBe(true);
    expect(applyRuntimeStep(runtime, "node-1", "step-1", "running", 1, 1)).toBe(false);
    expect(applyRuntimeStep(runtime, "node-1", "step-1", "running", 2, 2)).toBe(true);
    expect(applyRuntimeStep(runtime, "node-1", "step-2", "running", 1, 3)).toBe(true);
    expect(runtime.get("node-1")?.executionCount).toBe(3);
  });

  test("calculates total and active runtime from per-step timings", () => {
    const runtime = runtimeFromSteps([
      step({
        step_id: "step-1",
        status: "succeeded",
        started_at: "2026-01-01T00:00:00Z",
        finished_at: "2026-01-01T00:00:02Z",
        updated_at: "2026-01-01T00:00:02Z",
      }),
      step({
        step_id: "step-2",
        status: "running",
        started_at: "2026-01-01T00:00:03Z",
        updated_at: "2026-01-01T00:00:03Z",
      }),
    ]).get("node-1");

    expect(runtime).toBeDefined();
    expect(runtimeDurations(runtime, Date.parse("2026-01-01T00:00:05Z"))).toEqual({
      totalMs: 4_000,
      currentMs: 2_000,
    });
    expect(formatRuntimeDuration(4_000)).toBe("4.0s");
  });

  test("separates cumulative runtime from the current retry attempt", () => {
    const runtime = runtimeFromExecution(
      [step({
        status: "running",
        attempt: 1,
        started_at: new Date(1_000).toISOString(),
        updated_at: new Date(1_000).toISOString(),
      })],
      [
        runtimeEvent({
          id: "retry",
          type: "nodes.retry",
          timestamp: new Date(3_500).toISOString(),
          payload: { attempt: 1, next_attempt: 2, delay: "1s" },
        }),
      ],
      "run-1"
    );

    const runningDurations = runtimeDurations(runtime.get("node-1"), 5_500);
    expect(runningDurations).toEqual({
      totalMs: 3_500,
      currentMs: 1_000,
    });
    expect(`${formatRuntimeDuration(runningDurations.totalMs)}/${formatRuntimeDuration(runningDurations.currentMs)}`)
      .toBe("3.5s/1.0s");

    applyRuntimeStep(runtime, "node-1", "step-1", "succeeded", 2, 7_000, "", {
      scope: "attempt",
      finishedAt: 7_000,
    });
    expect(runtimeDurations(runtime.get("node-1"), 8_000)).toEqual({
      totalMs: 5_000,
      currentMs: 0,
    });
  });

  test("applies runtime snapshots only to real nodes", () => {
    const real = flowNode("node-1");
    const virtual = flowNode(START_NODE_REF, { virtualKind: "start" });
    const updated = applyRuntimeSnapshot(
      [real, virtual],
      new Map([
        ["node-1", { status: "failed", executionCount: 2, at: 1 }],
        [START_NODE_REF, { status: "failed", executionCount: 2, at: 1 }],
      ])
    );
    expect(updated[0].data).toMatchObject({ status: "failed", executionCount: 2 });
    expect(updated[1]).toBe(virtual);
  });

  test("lays out virtual boundaries around the graph and preserves saved positions", () => {
    const definition: GraphDefinition = {
      nodes: [
        { id: "first", type: "task" },
        { id: "second", type: "task" },
      ],
      edges: [{ from: "first", to: "second" }],
      entry_point: "first",
      finish_point: "second",
      metadata: { web: { positions: { first: { x: 25, y: 40 } } } },
    };
    const positions = layoutNodes(definition, new Set([START_NODE_REF, END_NODE_REF]));
    expect(positions.get(START_NODE_REF)?.x).toBe(-260);
    expect(positions.get("first")).toEqual({ x: 25, y: 40 });
    expect(positions.get("second")?.x).toBe(260);
    expect(positions.get(END_NODE_REF)?.x).toBe(520);
  });

  test("classifies virtual nodes and node event statuses", () => {
    expect(isVirtualStartNodeID(`${START_NODE_REF}:2`)).toBe(true);
    expect(isVirtualEndNodeID(`${END_NODE_REF}:2`)).toBe(true);
    expect(virtualNodeSpec(`${START_NODE_REF}:2`)).toMatchObject({ name: "Start 2", type: "start" });
    expect(runtimeStatusFromEvent("nodes.retry")).toBe("running");
    expect(runtimeStatusFromEvent("nodes.canceled")).toBe("canceled");
    expect(runtimeStatusFromEvent("run.finished")).toBe("");
    expect(eventAttempt("nodes.started", { node_name: "Node 1" })).toBe(1);
    expect(eventAttempt("nodes.retry", { attempt: 1, next_attempt: 2 })).toBe(2);
    expect(eventAttemptStartedAt("nodes.retry", { delay: "1m2.5s" }, 1_000)).toBe(63_500);
  });
});
