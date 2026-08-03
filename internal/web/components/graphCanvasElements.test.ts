import { describe, expect, test } from "bun:test";
import type { GraphDefinition, TriggerCanvasNode } from "../types";
import { END_NODE_REF, START_NODE_REF, graphEdgeId } from "../lib/graphEditor";
import {
  buildGraphCanvasElements,
  type GraphCanvasElementOptions,
} from "./graphCanvasElements";
import { triggerTargetHandleID } from "./graphCanvasModel";

function options(
  definition: GraphDefinition | null,
  overrides: Partial<GraphCanvasElementOptions> = {}
): GraphCanvasElementOptions {
  return {
    definition,
    editable: true,
    interactive: true,
    highlightedNodeIDs: new Set(),
    nodeTypes: [],
    runtime: new Map(),
    triggerNodes: [],
    virtualEdges: [],
    virtualLoops: [],
    virtualNodeIDs: [START_NODE_REF, END_NODE_REF],
    ...overrides,
  };
}

function triggerNode(enabled = true): TriggerCanvasNode {
  return {
    canvas_id: "trigger:trigger-1",
    label: "Inbound webhook",
    position: { x: -500, y: 80 },
    valid: true,
    trigger: {
      id: "trigger-1",
      name: "Inbound webhook",
      type: "webhook",
      enabled,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    },
  };
}

describe("graph canvas elements", () => {
  test("projects real, virtual, and Trigger nodes without changing their roles", () => {
    const definition: GraphDefinition = {
      entry_point: "task",
      finish_point: "task",
      nodes: [{ id: "task", name: "Process request", type: "task" }],
      metadata: { web: { positions: { task: { x: 20, y: 40 } } } },
    };
    const elements = buildGraphCanvasElements(options(definition, {
      highlightedNodeIDs: new Set(["task"]),
      runtime: new Map([["task", { status: "running", attempt: 2, at: 1 }]]),
      selectedNodeID: "task",
      selectedTriggerID: "trigger-1",
      triggerNodes: [triggerNode()],
    }));

    expect(elements.nodes).toHaveLength(4);
    expect(elements.nodes.find((node) => node.id === START_NODE_REF)?.data).toMatchObject({
      label: "Start",
      status: "idle",
      virtualKind: "start",
    });
    expect(elements.nodes.find((node) => node.id === "task")).toMatchObject({
      position: { x: 20, y: 40 },
      selected: true,
      data: {
        label: "Process request",
        status: "running",
        attempt: 2,
        highlighted: true,
      },
    });
    expect(elements.nodes.find((node) => node.id === END_NODE_REF)?.data.virtualKind).toBe("end");
    expect(elements.nodes.find((node) => node.id === "trigger:trigger-1")).toMatchObject({
      position: { x: -500, y: 80 },
      selected: true,
      data: {
        label: "Inbound webhook",
        virtualKind: "trigger",
        triggerID: "trigger-1",
        triggerEnabled: true,
        triggerValid: true,
      },
    });
  });

  test("summarizes static and dynamic state bindings and flags missing requirements", () => {
    const definition: GraphDefinition = {
      nodes: [{
        id: "worker",
        type: "worker",
        state: { input_1: { path: "shared.items.first" } },
      }],
    };
    const elements = buildGraphCanvasElements(options(definition, {
      nodeTypes: [{
        type: "worker",
        state_ports: [
          { name: "request", required: true },
          { name: "result", required: true, default_path: "scopes.{node_id}.result" },
        ],
        dynamic_state_ports: {
          name_pattern: "input_[0-9]+",
          min_ports: 2,
          schema: {},
          mode: "read",
          merge_strategy: "replace",
        },
      }],
      virtualNodeIDs: [],
    }));

    expect(elements.nodes[0].data).toMatchObject({
      bindingSummary: "2/3 state",
      missingBindings: true,
    });
  });

  test("adds labels and visual emphasis to conditional edges", () => {
    const condition = { type: "status_equals", config: { status: "ready" } };
    const definition: GraphDefinition = {
      nodes: [
        { id: "source", type: "task" },
        { id: "target", type: "task" },
      ],
      edges: [{ from: "source", to: "target", condition }],
    };
    const edgeID = graphEdgeId(definition.edges![0], 0);
    const unselected = buildGraphCanvasElements(options(definition, { virtualNodeIDs: [] })).edges[0];
    const selected = buildGraphCanvasElements(options(definition, {
      selectedEdgeID: edgeID,
      virtualNodeIDs: [],
    })).edges[0];

    expect(unselected).toMatchObject({
      id: edgeID,
      label: "status = ready",
      animated: true,
      selected: false,
      style: { stroke: "#8b5cf6", strokeWidth: 1.4 },
    });
    expect(unselected.labelBgPadding).toEqual([7, 4]);
    expect(selected).toMatchObject({
      selected: true,
      style: { stroke: "var(--flow-edge-selected)", strokeWidth: 2.6 },
    });
  });

  test("connects Trigger projections to the first visible Start node", () => {
    const definition: GraphDefinition = {
      entry_point: "task",
      nodes: [{ id: "task", type: "task" }],
    };
    const elements = buildGraphCanvasElements(options(definition, {
      triggerNodes: [triggerNode(false)],
      virtualNodeIDs: [`${START_NODE_REF}:2`, START_NODE_REF],
    }));
    const edge = elements.edges.find((item) => item.id === "trigger-edge:trigger:trigger-1");

    expect(edge).toMatchObject({
      source: "trigger:trigger-1",
      target: `${START_NODE_REF}:2`,
      targetHandle: triggerTargetHandleID,
      selectable: false,
      data: { triggerEdge: true },
      style: { strokeDasharray: "6 5", opacity: 0.4 },
    });
  });

  test("returns empty elements when no graph definition is loaded", () => {
    expect(buildGraphCanvasElements(options(null))).toEqual({ nodes: [], edges: [] });
  });
});
