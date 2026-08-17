import { describe, expect, test } from "bun:test";
import type { VirtualGraphLoop } from "../../../components/GraphCanvas";
import { graphNodePositions, withNodePosition } from "../../../lib/graphEditor";
import type { GraphDefinition } from "../../../types";
import {
  deleteGraphWorkspaceLoop,
  moveGraphWorkspaceLoop,
  updateGraphWorkspaceLoop,
} from "./graphWorkspaceLoopModel";

function graph(): GraphDefinition {
  return {
    version: "1.0",
    nodes: [
      { id: "task", type: "task" },
      { id: "review", type: "task" },
    ],
  };
}

describe("graph workspace loop model", () => {
  test("normalizes manual loop updates and protects automatic loops", () => {
    const manual: VirtualGraphLoop = { id: "manual", nodeIds: ["task"] };
    const state = { definition: graph(), virtualLoops: [manual] };
    const updated = updateGraphWorkspaceLoop(state, manual, (loop) => ({
      ...loop,
      name: " Review ",
      nodeIds: [" task ", "task", "review"],
    }));
    expect(updated.virtualLoops).toEqual([
      { id: "manual", name: "Review", nodeIds: ["task", "review"] },
    ]);

    const automatic: VirtualGraphLoop = { id: "automatic", nodeIds: ["task"], automatic: true };
    expect(updateGraphWorkspaceLoop(state, automatic, (loop) => loop)).toMatchObject({
      virtualLoops: [manual],
      message: "automatic loop follows graph edges",
    });
  });

  test("deletes only manual loops and clears their selection", () => {
    const manual: VirtualGraphLoop = { id: "manual", nodeIds: ["task"] };
    const automatic: VirtualGraphLoop = { id: "automatic", nodeIds: ["review"], automatic: true };
    const state = { definition: graph(), virtualLoops: [manual] };
    expect(deleteGraphWorkspaceLoop(state, [manual, automatic], "manual", "manual")).toMatchObject({
      virtualLoops: [],
      selectedLoopID: null,
      message: "loop deleted",
    });
    expect(deleteGraphWorkspaceLoop(state, [manual, automatic], "automatic", "automatic")).toMatchObject({
      virtualLoops: [manual],
      selectedLoopID: "automatic",
      message: "automatic loop follows graph edges",
    });
  });

  test("moves all positioned loop members by one canvas delta", () => {
    let definition = withNodePosition(graph(), "task", { x: 10, y: 20 });
    definition = withNodePosition(definition, "review", { x: 40, y: 60 });
    const loop: VirtualGraphLoop = { id: "loop", nodeIds: ["task", "review"] };
    const result = moveGraphWorkspaceLoop(
      { definition, virtualLoops: [loop] },
      [loop],
      loop.id,
      { x: 5, y: -10 }
    );
    const positions = graphNodePositions(result.definition!);
    expect(positions.get("task")).toEqual({ x: 15, y: 10 });
    expect(positions.get("review")).toEqual({ x: 45, y: 50 });
  });
});
