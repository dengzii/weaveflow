import { describe, expect, test } from "bun:test";
import type { Node } from "@xyflow/react";
import type { GraphDefinition, StepRecord } from "../types";
import { END_NODE_REF, START_NODE_REF } from "../lib/graphEditor";
import {
  applyRuntime,
  applyRuntimeSnapshot,
  eventErrorMessage,
  isVirtualEndNodeID,
  isVirtualStartNodeID,
  layoutNodes,
  runtimeFromSteps,
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
      attempt: 2,
      at: Date.parse("2026-01-01T00:02:00Z"),
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
      ["node-1", { status: "succeeded", attempt: 1, at: 20 }],
    ]);
    expect(applyRuntime(runtime, "node-1", "running", 2, 10)).toBe(true);
    expect(runtime.get("node-1")).toEqual({ status: "succeeded", attempt: 2, at: 20 });
    expect(applyRuntime(runtime, "node-1", "running", 2, 10)).toBe(false);
  });

  test("applies runtime snapshots only to real nodes", () => {
    const real = flowNode("node-1");
    const virtual = flowNode(START_NODE_REF, { virtualKind: "start" });
    const updated = applyRuntimeSnapshot(
      [real, virtual],
      new Map([
        ["node-1", { status: "failed", attempt: 2, at: 1 }],
        [START_NODE_REF, { status: "failed", attempt: 2, at: 1 }],
      ])
    );
    expect(updated[0].data).toMatchObject({ status: "failed", attempt: 2 });
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
  });
});
