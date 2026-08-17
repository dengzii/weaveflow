import { describe, expect, test } from "bun:test";
import type { GraphDefinition } from "../types";
import { graphEdgeId } from "./graphEditor";
import {
  analyzeVirtualGraphLoop,
  conditionDisplayLabel,
  detectVirtualGraphLoops,
  edgeSegmentsForLoopDisplay,
  graphEdgesForLoopDisplay,
  loopContinueHandleId,
  loopEndHandleId,
  loopEndInnerHandleId,
  loopStartHandleId,
  loopStartInnerHandleId,
  mergeVirtualGraphLoops,
  type VirtualGraphLoop,
} from "./loopPresentation";

describe("loop presentation", () => {
  test("detects and presents an LLM turn and tool execution cycle without mutating the graph", () => {
    const definition = toolLoopGraph();
    const original = JSON.parse(JSON.stringify(definition));

    const loops = detectVirtualGraphLoops(definition);
    expect(loops).toHaveLength(1);
    expect(loops[0]).toMatchObject({
      id: "loop:auto:llm",
      name: "Loop",
      nodeIds: ["llm", "tools"],
      automatic: true,
    });

    const analysis = analyzeVirtualGraphLoop(definition, loops[0]);
    expect(analysis.loopStartId).toBe("llm");
    expect(analysis.loopEndIds).toEqual(["llm"]);
    expect(analysis.conditionNodeIds).toEqual(["llm"]);
    expect(analysis.conditionLabels).toEqual(["continue · conversation has tool calls"]);
    expect(analysis.nextNodeIds).toEqual(["_end"]);
    expect([...analysis.backEdgeIds]).toEqual([graphEdgeId(definition.edges![3], 3)]);

    const displayEdges = graphEdgesForLoopDisplay(definition, loops);
    const entryEdgeId = graphEdgeId(definition.edges![0], 0);
    const exitEdgeId = graphEdgeId(definition.edges![2], 2);
    const backEdgeId = graphEdgeId(definition.edges![3], 3);
    expect(displayEdges.find(({ id }) => id === entryEdgeId)).toMatchObject({
      source: "input",
      target: "loop:auto:llm",
      targetHandle: loopStartHandleId,
    });
    expect(displayEdges.find(({ id }) => id === `${entryEdgeId}:loop-start`)).toMatchObject({
      selectionId: entryEdgeId,
      source: "loop:auto:llm",
      target: "llm",
      sourceHandle: loopStartInnerHandleId,
      contained: true,
    });
    expect(displayEdges.find(({ id }) => id === exitEdgeId)).toMatchObject({
      source: "loop:auto:llm",
      target: "_end",
      sourceHandle: loopEndHandleId,
    });
    expect(displayEdges.find(({ id }) => id === `${exitEdgeId}:loop-end`)).toMatchObject({
      selectionId: exitEdgeId,
      source: "llm",
      target: "loop:auto:llm",
      targetHandle: loopEndInnerHandleId,
      contained: true,
    });
    expect(displayEdges.find(({ id }) => id === graphEdgeId(definition.edges![1], 1))).toMatchObject({
      source: "llm",
      target: "tools",
    });
    expect(displayEdges.find(({ id }) => id === backEdgeId)).toMatchObject({
      source: "tools",
      target: "loop:auto:llm",
      targetHandle: loopContinueHandleId,
      contained: true,
    });
    expect(definition).toEqual(original);
  });

  test("does not create groups for acyclic graphs", () => {
    const definition: GraphDefinition = {
      entry_point: "one",
      finish_point: "two",
      nodes: [
        { id: "one", type: "llm_turn", config: {} },
        { id: "two", type: "tool_execution", config: {} },
      ],
      edges: [{ from: "one", to: "two" }],
    };
    expect(detectVirtualGraphLoops(definition)).toEqual([]);
  });

  test("keeps explicit UI groups ahead of overlapping automatic groups", () => {
    const automatic = detectVirtualGraphLoops(toolLoopGraph());
    const explicit: VirtualGraphLoop = { id: "loop", name: "custom loop", nodeIds: ["llm", "tools"] };
    expect(mergeVirtualGraphLoops([explicit], automatic)).toEqual([explicit]);
  });

  test("bridges virtual entry and finish edges through the loop ports", () => {
    const definition = toolLoopGraph();
    const loops = detectVirtualGraphLoops(definition);

    expect(edgeSegmentsForLoopDisplay(
      definition,
      { from: "__start__", to: "llm" },
      "virtual:entry",
      loops
    )).toMatchObject([
      {
        source: "__start__",
        target: "loop:auto:llm",
        targetHandle: loopStartHandleId,
      },
      {
        selectionId: "virtual:entry",
        source: "loop:auto:llm",
        target: "llm",
        sourceHandle: loopStartInnerHandleId,
        contained: true,
      },
    ]);

    expect(edgeSegmentsForLoopDisplay(
      definition,
      { from: "llm", to: "__end__" },
      "virtual:finish",
      loops
    )).toMatchObject([
      {
        selectionId: "virtual:finish",
        source: "llm",
        target: "loop:auto:llm",
        targetHandle: loopEndInnerHandleId,
        contained: true,
      },
      {
        source: "loop:auto:llm",
        target: "__end__",
        sourceHandle: loopEndHandleId,
      },
    ]);
  });

  test("makes configured conditions readable", () => {
    expect(conditionDisplayLabel({ type: "plan_status_equals", config: { status: "done" } })).toBe("plan status = done");
    expect(conditionDisplayLabel({
      type: "expression_conditions",
      config: { match: "any", expressions: [{ value1: "status", op: "equals", value2: "ready" }] },
    })).toBe("any expressions (1)");
  });
});

function toolLoopGraph(): GraphDefinition {
  return {
    version: "1.0",
    name: "shared_llm_turn_tool_execution_condition_loop",
    entry_point: "input",
    nodes: [
      { id: "input", type: "user_input", config: {} },
      { id: "llm", type: "llm_turn", config: {} },
      { id: "tools", type: "tool_execution", config: {} },
    ],
    edges: [
      { from: "input", to: "llm" },
      { from: "llm", to: "tools", condition: { type: "conversation_has_tool_calls" } },
      { from: "llm", to: "_end" },
      { from: "tools", to: "llm" },
    ],
  };
}
