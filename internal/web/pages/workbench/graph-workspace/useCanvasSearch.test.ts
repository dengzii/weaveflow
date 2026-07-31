import { describe, expect, test } from "bun:test";
import type { GraphNodeSpec } from "../../../types";
import { matchingCanvasNodes, nextCanvasSearchIndex } from "./useCanvasSearch";

const nodes: GraphNodeSpec[] = [
  { id: "fetch_user", name: "Load profile", type: "http", description: "Reads account details" },
  { id: "summarize", name: "Summarize", type: "llm" },
];

describe("canvas search model", () => {
  test("matches normalized node identity and descriptive fields", () => {
    expect(matchingCanvasNodes(nodes, " FETCH ").map((node) => node.id)).toEqual(["fetch_user"]);
    expect(matchingCanvasNodes(nodes, "profile").map((node) => node.id)).toEqual(["fetch_user"]);
    expect(matchingCanvasNodes(nodes, "ACCOUNT").map((node) => node.id)).toEqual(["fetch_user"]);
    expect(matchingCanvasNodes(nodes, "llm").map((node) => node.id)).toEqual(["summarize"]);
    expect(matchingCanvasNodes(nodes, "  ")).toEqual([]);
  });

  test("wraps search navigation in both directions", () => {
    expect(nextCanvasSearchIndex(0, 1, 2)).toBe(1);
    expect(nextCanvasSearchIndex(1, 1, 2)).toBe(0);
    expect(nextCanvasSearchIndex(0, -1, 2)).toBe(1);
    expect(nextCanvasSearchIndex(4, 1, 0)).toBe(0);
  });
});
