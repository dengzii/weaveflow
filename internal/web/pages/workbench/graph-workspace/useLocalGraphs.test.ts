import { describe, expect, test } from "bun:test";
import {
  cacheServerGraphs,
  hydrateServerGraph,
  preferredServerGraph,
  readRememberedGraphID,
  readLocalGraphs,
  rememberGraphID,
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
    },
    virtualNodeIDs: ["__start__", "__end__"],
    virtualEdges: [{ id: "entry", from: "__start__", to: "task", kind: "entry" }],
    virtualLoops: [{ id: "loop-1", name: "Retry", nodeIds: ["task"] }],
    validTriggerIDs: ["keep"],
  };
}

describe("local graphs", () => {
  test("remembers a selected graph and prefers it when the server still lists it", () => {
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
    };
    const graphs = [
      {
        id: "graph-1",
        graph_version: "2.0",
        node_count: 1,
        session_count: 1,
        latest_session: "session-1",
        active_run_count: 0,
        updated_at: "2026-08-04T01:02:03Z",
      },
      {
        id: "graph-2",
        graph_version: "2.0",
        node_count: 2,
        session_count: 1,
        latest_session: "session-2",
        active_run_count: 0,
        updated_at: "2026-08-04T02:03:04Z",
      },
    ];

    rememberGraphID(" graph-2 ", storage);

    expect(readRememberedGraphID(storage)).toBe("graph-2");
    expect(preferredServerGraph(graphs, readRememberedGraphID(storage))?.id).toBe("graph-2");
    expect(preferredServerGraph(graphs, "missing")?.id).toBe("graph-1");
  });

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
      nodeCount: input.definition.nodes.length,
      serverGraph: false,
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

  test("loads server summaries before hydrating detail and saves edits only in memory", () => {
    const [serverGraph] = cacheServerGraphs([{
      id: "graph-1",
      name: "workflow",
      graph_version: "2.0",
      node_count: 1,
      session_count: 2,
      latest_session: "20260804T010203.000000000Z",
      active_run_count: 0,
      updated_at: "2026-08-04T01:02:03Z",
    }]);

    expect(serverGraph).toMatchObject({
      graphId: "graph-1",
      graphVersion: "2.0",
      title: "workflow",
      nodeCount: 1,
      updatedAt: "2026-08-04T01:02:03Z",
    });
    expect(serverGraph.definition).toBeUndefined();

    const hydrated = hydrateServerGraph(serverGraph, {
      graph: {
        id: "graph-1",
        version: "2.0",
        graph_session_id: "20260804T010203.000000000Z",
      },
      definition: snapshot().definition!,
      settings: snapshot().runtimeSettings,
      initial_state_requirements: { required: [], provided_by_entry: [], provided_by_upstream: [], unresolved: [] },
      latest_session: {
        id: "20260804T010203.000000000Z",
        created_at: "2026-08-04T01:02:03Z",
      },
      active: { active_run_count: 0 },
    });

    const saved = saveLocalGraph({
      id: hydrated.id,
      graphId: hydrated.graphId,
      graphVersion: hydrated.graphVersion,
      definition: { ...hydrated.definition, name: "edited" },
      runtimeSettings: hydrated.runtimeSettings,
    });
    expect(saved.title).toBe("edited");
    expect(readLocalGraphs()).toHaveLength(1);
    expect(readLocalGraphs()[0].definition?.name).toBe("edited");

    cacheServerGraphs([]);
  });
});
