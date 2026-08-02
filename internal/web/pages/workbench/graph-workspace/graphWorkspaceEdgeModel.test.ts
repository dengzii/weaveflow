import { describe, expect, test } from "bun:test";
import type { VirtualGraphEdge } from "../../../components/graphCanvasElements";
import { END_NODE_REF, START_NODE_REF, graphEdgeId } from "../../../lib/graphEditor";
import type { GraphDefinition } from "../../../types";
import { virtualEdgesFromDefinition } from "./graphWorkspaceModel";
import {
  connectGraphWorkspaceNodes,
  deleteGraphWorkspaceEdge,
  removeGraphWorkspaceVirtualEdgesForNode,
  updateGraphWorkspaceEdge,
  updateGraphWorkspaceVirtualEdge,
} from "./graphWorkspaceEdgeModel";

function graph(overrides: Partial<GraphDefinition> = {}): GraphDefinition {
  return {
    version: "2.0",
    nodes: [
      { id: "task", type: "task" },
      { id: "review", type: "task" },
    ],
    ...overrides,
  };
}

describe("graph workspace edge model", () => {
  test("connects Start, End, and regular nodes using their runtime representations", () => {
    const initial = { definition: graph({ finish_point: "task" }), virtualEdges: [], displayVirtualEdges: [] };
    const entry = connectGraphWorkspaceNodes(initial, START_NODE_REF, "task");
    expect(entry.definition?.entry_point).toBe("task");
    expect(entry.virtualEdges).toEqual([
      { id: "virtual:entry:__start__->task", from: START_NODE_REF, to: "task", kind: "entry" },
    ]);
    expect(entry.message).toBe("entry connected");

    const finish = connectGraphWorkspaceNodes(initial, "task", `${END_NODE_REF}:2`);
    expect(finish.definition?.edges).toEqual([{ from: "task", to: END_NODE_REF }]);
    expect(finish.definition?.finish_point).toBeUndefined();
    expect(finish.selectedEdgeID).toBe("task->__end__:direct:0");

    const regular = connectGraphWorkspaceNodes(initial, "task", "review");
    expect(regular.definition?.edges).toEqual([{ from: "task", to: "review" }]);
    expect(regular.message).toBe("edge connected");
  });

  test("rejects invalid virtual connections and selects duplicate edges", () => {
    const definition = graph({
      entry_point: "task",
      edges: [{ from: "task", to: "review" }],
    });
    const displayVirtualEdges = virtualEdgesFromDefinition(definition, [START_NODE_REF, END_NODE_REF]);
    const state = { definition, virtualEdges: [], displayVirtualEdges };

    expect(connectGraphWorkspaceNodes(state, END_NODE_REF, "task").message).toBe("invalid virtual edge");
    expect(connectGraphWorkspaceNodes(state, START_NODE_REF, "task")).toMatchObject({
      selectedEdgeID: "virtual:entry:__start__->task",
      message: "edge already exists",
    });
    expect(connectGraphWorkspaceNodes(state, "task", "review")).toMatchObject({
      selectedEdgeID: graphEdgeId(definition.edges![0], 0),
      message: "edge already exists",
    });
  });

  test("updates real edges and clears a conflicting finish point", () => {
    const definition = graph({
      finish_point: "review",
      edges: [{ from: "task", to: "review" }],
    });
    const selectedEdgeID = graphEdgeId(definition.edges![0], 0);
    const result = updateGraphWorkspaceEdge(
      { definition, virtualEdges: [] },
      selectedEdgeID,
      (edge) => ({ ...edge, from: "review", to: END_NODE_REF })
    );

    expect(result.definition?.edges).toEqual([{ from: "review", to: END_NODE_REF }]);
    expect(result.definition?.finish_point).toBeUndefined();
    expect(result.message).toBe("edge updated");
  });

  test("updates entry edges and converts finish edges to runtime graph edges", () => {
    const entryEdge: VirtualGraphEdge = {
      id: "entry",
      from: START_NODE_REF,
      to: "task",
      kind: "entry",
    };
    const entry = updateGraphWorkspaceVirtualEdge(
      { definition: graph({ entry_point: "task" }), virtualEdges: [entryEdge] },
      entryEdge,
      (edge) => ({ ...edge, to: "review" })
    );
    expect(entry.definition?.entry_point).toBe("review");
    expect(entry.virtualEdges[0]).toMatchObject({ to: "review", kind: "entry" });

    const finishEdge: VirtualGraphEdge = {
      id: "finish",
      from: "task",
      to: `${END_NODE_REF}:2`,
      kind: "finish",
    };
    const finish = updateGraphWorkspaceVirtualEdge(
      { definition: graph({ finish_point: "task" }), virtualEdges: [finishEdge] },
      finishEdge,
      (edge) => ({ ...edge, condition: { type: "approved" } })
    );
    expect(finish.definition?.edges).toEqual([
      { from: "task", to: END_NODE_REF, condition: { type: "approved" } },
    ]);
    expect(finish.definition?.finish_point).toBeUndefined();
    expect(finish.virtualEdges).toEqual([]);
  });

  test("rejects invalid virtual edge shapes and duplicate entry targets", () => {
    const selected: VirtualGraphEdge = {
      id: "entry",
      from: START_NODE_REF,
      to: "task",
      kind: "entry",
    };
    const state = { definition: graph({ entry_point: "review" }), virtualEdges: [selected] };
    expect(
      updateGraphWorkspaceVirtualEdge(state, selected, (edge) => ({ ...edge, from: "task" })).message
    ).toBe("invalid entry edge");
    expect(
      updateGraphWorkspaceVirtualEdge(state, selected, (edge) => ({ ...edge, to: "review" })).message
    ).toBe("edge already exists");
  });

  test("deletes virtual edges with deterministic entry fallback and removes real edges", () => {
    const semantic: VirtualGraphEdge = {
      id: "virtual:entry:__start__->task",
      from: START_NODE_REF,
      to: "task",
      kind: "entry",
    };
    const fallback: VirtualGraphEdge = {
      id: "fallback",
      from: `${START_NODE_REF}:2`,
      to: "review",
      kind: "entry",
    };
    const virtualResult = deleteGraphWorkspaceEdge(
      {
        definition: graph({ entry_point: "task" }),
        virtualEdges: [fallback],
        displayVirtualEdges: [semantic, fallback],
      },
      semantic.id
    );
    expect(virtualResult.definition?.entry_point).toBe("review");
    expect(virtualResult.virtualEdges).toEqual([fallback]);

    const definition = graph({ edges: [{ from: "task", to: "review" }] });
    const realResult = deleteGraphWorkspaceEdge(
      { definition, virtualEdges: [], displayVirtualEdges: [] },
      graphEdgeId(definition.edges![0], 0)
    );
    expect(realResult.definition?.edges).toEqual([]);
  });

  test("removes all virtual boundary edges attached to a deleted node", () => {
    const entry: VirtualGraphEdge = { id: "entry", from: START_NODE_REF, to: "task", kind: "entry" };
    const finish: VirtualGraphEdge = { id: "finish", from: "task", to: END_NODE_REF, kind: "finish" };
    const result = removeGraphWorkspaceVirtualEdgesForNode(
      {
        definition: graph({ entry_point: "task", finish_point: "task" }),
        virtualEdges: [entry, finish],
        displayVirtualEdges: [entry, finish],
      },
      "task"
    );
    expect(result.virtualEdges).toEqual([]);
    expect(result.definition?.entry_point).toBeUndefined();
    expect(result.definition?.finish_point).toBeUndefined();
  });
});
