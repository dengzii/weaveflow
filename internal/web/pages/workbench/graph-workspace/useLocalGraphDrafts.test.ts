import { describe, expect, test } from "bun:test";
import type { LocalGraphDraft } from "../../../lib/localGraphs";
import {
  localGraphDraftActivation,
  localGraphDraftSaveInput,
  localGraphWorkspaceSignature,
  type LocalGraphWorkspaceSnapshot,
} from "./useLocalGraphDrafts";

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
    virtualNodeIDs: ["__start__", "__end__"],
    virtualEdges: [{ id: "entry", from: "__start__", to: "task", kind: "entry" }],
    virtualLoops: [{ id: "loop-1", name: "Retry", nodeIds: ["task"] }],
    validTriggerIDs: ["keep"],
  };
}

describe("local graph drafts", () => {
  test("builds a save input with canvas metadata and only valid Trigger positions", () => {
    const input = localGraphDraftSaveInput(snapshot(), "draft-1");

    expect(input).toMatchObject({
      id: "draft-1",
      title: "workflow",
      graphId: "graph-1",
      graphVersion: "2.0",
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

  test("restores persisted workspace state with the same saved signature", () => {
    const current = snapshot();
    const input = localGraphDraftSaveInput(current, "draft-1")!;
    const draft: LocalGraphDraft = {
      id: "draft-1",
      title: "Workflow",
      graphId: current.graphID,
      graphVersion: current.graphVersion,
      definition: input.definition,
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
    };

    const activation = localGraphDraftActivation(draft);
    expect(activation.workspaceState).toEqual({
      virtualNodeIDs: current.virtualNodeIDs,
      virtualEdges: current.virtualEdges,
      virtualLoops: current.virtualLoops,
    });
    expect(activation.signature).toBe(localGraphWorkspaceSignature({
      ...current,
      definition: input.definition,
      validTriggerIDs: undefined,
    }));
  });

  test("returns no signature or save input without a parsed definition", () => {
    const empty = { ...snapshot(), definition: null };
    expect(localGraphWorkspaceSignature(empty)).toBe("");
    expect(localGraphDraftSaveInput(empty, "")).toBeNull();
  });
});
