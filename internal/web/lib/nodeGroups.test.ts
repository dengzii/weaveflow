import { describe, expect, test } from "bun:test";
import type { NodeGroup, NodeTypeSchema } from "../types";
import { groupNodeTypes, partitionNodeTypes } from "./nodeGroups";

const nodeTypes: NodeTypeSchema[] = [
  { type: "agent", title: "Agent" },
  { type: "llm_turn", title: "LLM Turn" },
  { type: "custom", title: "Custom" },
];

describe("groupNodeTypes", () => {
  test("uses group and member order", () => {
    const groups: NodeGroup[] = [
      { name: "Models", node_types: ["llm_turn"] },
      { name: "Agents", node_types: ["agent"] },
    ];

    expect(groupNodeTypes(nodeTypes, groups)).toEqual([
      { name: "Models", nodeTypes: [nodeTypes[1]] },
      { name: "Agents", nodeTypes: [nodeTypes[0]] },
      { name: "Other", nodeTypes: [nodeTypes[2]] },
    ]);
  });

  test("ignores missing and duplicate group members", () => {
    const groups: NodeGroup[] = [
      { name: "Primary", node_types: ["missing", "agent"] },
      { name: "Duplicate", node_types: ["agent"] },
    ];

    expect(groupNodeTypes(nodeTypes, groups)).toEqual([
      { name: "Primary", nodeTypes: [nodeTypes[0]] },
      { name: "Other", nodeTypes: [nodeTypes[1], nodeTypes[2]] },
    ]);
  });

  test("keeps ungrouped node types separate for create menus", () => {
    const groups: NodeGroup[] = [{ name: "Models", node_types: ["llm_turn"] }];

    expect(partitionNodeTypes(nodeTypes, groups)).toEqual({
      groups: [{ name: "Models", nodeTypes: [nodeTypes[1]] }],
      ungroupedNodeTypes: [nodeTypes[0], nodeTypes[2]],
    });
  });
});
