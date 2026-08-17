import { describe, expect, test } from "bun:test";
import type { VirtualGraphEdge, VirtualGraphLoop } from "../../../components/GraphCanvas";
import {
  END_NODE_REF,
  START_NODE_REF,
  graphNodePositions,
} from "../../../lib/graphEditor";
import type { GraphDefinition, NodeTypeSchema } from "../../../types";
import {
  addGraphWorkspaceNode,
  deleteGraphWorkspaceNode,
  duplicateGraphWorkspaceNode,
  renameGraphWorkspaceNode,
  type GraphWorkspaceNodeState,
} from "./graphWorkspaceNodeModel";

const nodeType: NodeTypeSchema = {
  type: "task",
  title: "Task",
  config_schema: { type: "object", properties: { prompt: { type: "string", default: "go" } } },
};

function graph(overrides: Partial<GraphDefinition> = {}): GraphDefinition {
  return {
    version: "1.0",
    nodes: [
      { id: "task", name: "Task", type: "task", config: { nested: { value: 1 } } },
      { id: "review", name: "Review", type: "task" },
    ],
    ...overrides,
  };
}

function state(definition: GraphDefinition | null = graph()): GraphWorkspaceNodeState {
  return {
    definition,
    virtualNodeIDs: [START_NODE_REF, END_NODE_REF],
    virtualEdges: [],
    virtualLoops: [],
  };
}

describe("graph workspace node model", () => {
  test("creates the first real node and adds later nodes with stable selection", () => {
    const created = addGraphWorkspaceNode(state(null), nodeType, "sample graph", [], { x: 10, y: 20 });
    expect(created.definition).toMatchObject({
      version: "1.0",
      name: "sample_graph",
      entry_point: "task",
    });
    expect(created.selectedNodeID).toBe("task");
    expect(graphNodePositions(created.definition!).get("task")).toEqual({ x: 10, y: 20 });

    const added = addGraphWorkspaceNode(created, nodeType, "sample graph");
    expect(added.definition?.nodes.map((node) => node.id)).toEqual(["task", "task_2"]);
    expect(added.selectedNodeID).toBe("task_2");
  });

  test("adds numbered virtual boundary nodes without changing graph execution nodes", () => {
    const result = addGraphWorkspaceNode(
      state(),
      { type: "start", title: "Start" },
      "graph",
      [],
      { x: 30, y: 40 }
    );
    expect(result.virtualNodeIDs).toEqual([START_NODE_REF, END_NODE_REF, `${START_NODE_REF}:2`]);
    expect(result.definition?.nodes).toHaveLength(2);
    expect(graphNodePositions(result.definition!).get(`${START_NODE_REF}:2`)).toEqual({ x: 30, y: 40 });
  });

  test("renames node references and loop membership while rejecting duplicate IDs", () => {
    const loop: VirtualGraphLoop = { id: "review-loop", nodeIds: ["task", "review"] };
    const initial = { ...state(graph({ entry_point: "task", finish_point: "task" })), virtualLoops: [loop] };
    const renamed = renameGraphWorkspaceNode(initial, "task", "draft");
    expect(renamed.definition).toMatchObject({ entry_point: "draft", finish_point: "draft" });
    expect(renamed.virtualLoops[0].nodeIds).toEqual(["draft", "review"]);
    expect(renamed.selectedNodeID).toBe("draft");

    expect(renameGraphWorkspaceNode(initial, "task", "review")).toMatchObject({
      selectedNodeID: "task",
      message: "node id already exists",
    });
  });

  test("duplicates node-owned data and offsets the canvas position", () => {
    const initialDefinition = graph();
    initialDefinition.metadata = { web: { positions: { task: { x: 5, y: 8 } } } };
    const result = duplicateGraphWorkspaceNode(state(initialDefinition), "task");
    const copy = result.definition?.nodes.find((node) => node.id === "task_copy");
    expect(copy).toMatchObject({ name: "Task copy", config: { nested: { value: 1 } } });
    expect(graphNodePositions(result.definition!).get("task_copy")).toEqual({ x: 45, y: 48 });

    (copy!.config!.nested as { value: number }).value = 2;
    expect((result.definition!.nodes[0].config!.nested as { value: number }).value).toBe(1);
  });

  test("deletes a real node after applying persisted entry and finish fallbacks", () => {
    const semanticEntry: VirtualGraphEdge = {
      id: "entry",
      from: START_NODE_REF,
      to: "task",
      kind: "entry",
    };
    const semanticFinish: VirtualGraphEdge = {
      id: "finish",
      from: "task",
      to: END_NODE_REF,
      kind: "finish",
    };
    const fallbackEntry: VirtualGraphEdge = {
      id: "fallback-entry",
      from: `${START_NODE_REF}:2`,
      to: "review",
      kind: "entry",
    };
    const fallbackFinish: VirtualGraphEdge = {
      id: "fallback-finish",
      from: "review",
      to: `${END_NODE_REF}:2`,
      kind: "finish",
    };
    const result = deleteGraphWorkspaceNode(
      {
        ...state(graph({ entry_point: "task", finish_point: "task" })),
        virtualEdges: [fallbackEntry, fallbackFinish],
        virtualLoops: [{ id: "loop", nodeIds: ["task", "review"] }],
        displayVirtualEdges: [semanticEntry, semanticFinish, fallbackEntry, fallbackFinish],
      },
      "task"
    );
    expect(result.definition?.nodes.map((node) => node.id)).toEqual(["review"]);
    expect(result.definition).toMatchObject({ entry_point: "review", finish_point: "review" });
    expect(result.virtualLoops[0].nodeIds).toEqual(["review"]);
  });

  test("hides a virtual node and removes its attached persisted edge", () => {
    const extraStart = `${START_NODE_REF}:2`;
    const edge: VirtualGraphEdge = {
      id: "extra-entry",
      from: extraStart,
      to: "review",
      kind: "entry",
    };
    const result = deleteGraphWorkspaceNode(
      {
        ...state(),
        virtualNodeIDs: [START_NODE_REF, END_NODE_REF, extraStart],
        virtualEdges: [edge],
        displayVirtualEdges: [edge],
      },
      extraStart
    );
    expect(result.virtualNodeIDs).toEqual([START_NODE_REF, END_NODE_REF]);
    expect(result.virtualEdges).toEqual([]);
    expect(result.message).toBe("Start 2 hidden");
  });
});
