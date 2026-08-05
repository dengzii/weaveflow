import { describe, expect, test } from "bun:test";
import {
  cacheServerGraphs,
  readLocalGraphs,
  saveLocalGraph,
  type LocalGraph,
} from "../../../lib/localGraphs";
import {
  localGraphActivation,
  localGraphSaveInput,
  localGraphWorkspaceSignature,
  type LocalGraphWorkspaceSnapshot,
} from "./useLocalGraphs";

function snapshot(): LocalGraphWorkspaceSnapshot {
  return {
    definition: {
      name: "workflow",
      nodes: [{ id: "task", type: "task" }],
      metadata: {
        web: {
          trigger_nodes: {
            keep: { x: 10, y: 20 },
            remove: { x: 30, y: 40 },
          },
        },
      },
    },
    graphID: "graph-1",
    graphVersion: "2.0",
    runtimeSettings: {
      environment: { MODE: "test" },
      models: [],
      memory: { enabled: false },
    },
    virtualNodeIDs: ["__start__", "__end__"],
    virtualEdges: [{ id: "entry", from: "__start__", to: "task", kind: "entry" }],
    virtualLoops: [{ id: "loop-1", name: "Retry", nodeIds: ["task"] }],
    validTriggerIDs: ["keep"],
  };
}

describe("local graphs", () => {
  test("builds a save input with canvas metadata and only valid Trigger positions", () => {
    const input = localGraphSaveInput(snapshot(), "cache-1");

    expect(input).toMatchObject({
      id: "cache-1",
      title: "workflow",
      graphId: "graph-1",
      graphVersion: "2.0",
      runtimeSettings: snapshot().runtimeSettings,
      definition: {
        metadata: {
          web: {
            virtual_node_ids: ["__start__", "__end__"],
            virtual_edges: [{ id: "entry", from: "__start__", to: "task", kind: "entry" }],
            virtual_loops: [{ id: "loop-1", name: "Retry", node_ids: ["task"] }],
            trigger_nodes: { keep: { x: 10, y: 20 } },
          },
        },
      },
    });
  });

  test("restores cached workspace state with the same saved signature", () => {
    const current = snapshot();
    const input = localGraphSaveInput(current, "cache-1")!;
    const graph: LocalGraph = {
      id: "cache-1",
      title: "Workflow",
      graphId: current.graphID,
      graphVersion: current.graphVersion,
      definition: input.definition,
      runtimeSettings: current.runtimeSettings,
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
    };

    const activation = localGraphActivation(graph);
    expect(activation.workspaceState).toEqual({
      virtualNodeIDs: current.virtualNodeIDs,
      virtualEdges: current.virtualEdges,
      virtualLoops: current.virtualLoops,
    });
    expect(activation.runtimeSettings).toEqual(current.runtimeSettings);
    expect(activation.signature).toBe(localGraphWorkspaceSignature({
      ...current,
      definition: input.definition,
      validTriggerIDs: undefined,
    }));
  });

  test("returns no signature or save input without a parsed definition", () => {
    const empty = { ...snapshot(), definition: null };
    expect(localGraphWorkspaceSignature(empty)).toBe("");
    expect(localGraphSaveInput(empty, "")).toBeNull();
  });

  test("hydrates server graphs and saves edits only in memory", () => {
    const [serverGraph] = cacheServerGraphs([{
      id: "graph-1",
      graph_version: "2.0",
      definition: snapshot().definition!,
      settings: snapshot().runtimeSettings,
      session_count: 2,
      latest_session: "20260804T010203.000000000Z",
      updated_at: "2026-08-04T01:02:03Z",
    }]);

    expect(serverGraph).toMatchObject({
      graphId: "graph-1",
      graphVersion: "2.0",
      title: "workflow",
      updatedAt: "2026-08-04T01:02:03Z",
    });

    const saved = saveLocalGraph({
      id: serverGraph.id,
      graphId: serverGraph.graphId,
      graphVersion: serverGraph.graphVersion,
      definition: { ...serverGraph.definition, name: "edited" },
      runtimeSettings: serverGraph.runtimeSettings,
    });
    expect(saved.title).toBe("edited");
    expect(readLocalGraphs()).toHaveLength(1);
    expect(readLocalGraphs()[0].definition.name).toBe("edited");

    cacheServerGraphs([]);
  });
});
