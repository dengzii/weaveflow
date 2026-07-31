import { describe, expect, test } from "bun:test";
import type { VirtualGraphEdge } from "../../../components/GraphCanvas";
import type { GraphDefinition } from "../../../types";
import { defaultVirtualNodeIds } from "./constants";
import {
  autoSaveSignature,
  cloneJSONRecord,
  graphCanvasViewportStorageKey,
  graphScriptBadgeCount,
  mergeVirtualEdges,
  normalizeVirtualLoop,
  savedGraphWorkspaceState,
  uniqueNodeID,
  upsertVirtualEdge,
  virtualEdgesFromDefinition,
  withSavedGraphWorkspaceState,
} from "./graphWorkspaceModel";

describe("graph workspace model", () => {
  test("builds stable, encoded viewport keys with definition fallbacks", () => {
    expect(graphCanvasViewportStorageKey("graph one", "v1", "draft/a", null)).toBe("draft%2Fa:graph%20one:v1");
    expect(graphCanvasViewportStorageKey("", "", "", { name: "fallback", version: "2.0", nodes: [] })).toBe(
      "server:fallback:2.0"
    );
  });

  test("counts supported script and hook shapes", () => {
    const definition: GraphDefinition = {
      nodes: [],
      metadata: {
        pre_script: "prepare()",
        scripts: { post: ["finish()", ""] },
        web: { hooks: { before: { source: "validate()" } } },
      },
    };
    expect(graphScriptBadgeCount(definition)).toBe(3);
    expect(graphScriptBadgeCount(null)).toBe(0);
  });

  test("derives semantic entry and finish edges and lets saved edges override them", () => {
    const definition: GraphDefinition = {
      nodes: [{ id: "entry", type: "task" }],
      entry_point: "entry",
      finish_point: "entry",
    };
    const semantic = virtualEdgesFromDefinition(definition, defaultVirtualNodeIds);
    expect(semantic.map((edge) => edge.kind)).toEqual(["entry", "finish"]);

    const configured = { ...semantic[0], condition: { type: "ready" } };
    expect(mergeVirtualEdges(semantic, [configured])[0].condition).toEqual({ type: "ready" });
  });

  test("upserts a virtual entry edge without keeping a conflicting source", () => {
    const previous: VirtualGraphEdge = { id: "old", from: "__start__", to: "one", kind: "entry" };
    const conflict: VirtualGraphEdge = { id: "conflict", from: "__start__", to: "two", kind: "entry" };
    const next: VirtualGraphEdge = { id: "next", from: "__start__", to: "three", kind: "entry" };
    expect(upsertVirtualEdge([previous, conflict], previous, next)).toEqual([next]);
  });

  test("round-trips workspace metadata and accepts legacy loop groups", () => {
    const definition: GraphDefinition = { nodes: [{ id: "one", type: "task" }], metadata: { owner: "team" } };
    const edge: VirtualGraphEdge = { id: "entry", from: "__start__", to: "one", kind: "entry" };
    const saved = withSavedGraphWorkspaceState(
      definition,
      ["__start__"],
      [edge],
      [{ id: " loop ", name: " Cycle ", nodeIds: ["one", "one"] }]
    );
    const restored = savedGraphWorkspaceState(saved);

    expect(saved.metadata?.owner).toBe("team");
    expect(restored.virtualNodeIDs).toEqual(["__start__"]);
    expect(restored.virtualEdges).toEqual([edge]);
    expect(restored.virtualLoops).toEqual([{ id: "loop", name: "Cycle", nodeIds: ["one"] }]);

    const legacy = savedGraphWorkspaceState({
      nodes: [],
      metadata: { web: { virtual_groups: [{ id: "legacy", node_ids: ["one"] }] } },
    });
    expect(legacy.virtualLoops).toEqual([{ id: "legacy", name: undefined, nodeIds: ["one"] }]);
    expect(savedGraphWorkspaceState({ nodes: [] }).virtualNodeIDs).toEqual(defaultVirtualNodeIds);
  });

  test("clones JSON records and creates deterministic unique node ids", () => {
    const source = { nested: { value: 1 } };
    const cloned = cloneJSONRecord(source);
    source.nested.value = 2;
    expect(cloned).toEqual({ nested: { value: 1 } });
    expect(uniqueNodeID("task", [{ id: "task" }, { id: "task_2" }])).toBe("task_3");
  });

  test("includes persisted workspace state in the autosave signature", () => {
    const definition: GraphDefinition = { nodes: [] };
    const first = autoSaveSignature(definition, "graph", "v1", ["__start__"], [], []);
    const second = autoSaveSignature(definition, "graph", "v1", ["__end__"], [], []);
    expect(first).not.toBe(second);
  });

  test("normalizes virtual loops", () => {
    expect(normalizeVirtualLoop({ id: " loop ", name: " Name ", nodeIds: [" a ", "a", ""] })).toEqual({
      id: "loop",
      name: "Name",
      nodeIds: ["a"],
    });
  });
});
