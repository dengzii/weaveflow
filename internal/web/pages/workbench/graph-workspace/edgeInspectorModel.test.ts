import { describe, expect, test } from "bun:test";
import type { VirtualGraphEdge } from "../../../components/GraphCanvas";
import type { ConditionSchema, GraphDefinition, GraphNodeSpec } from "../../../types";
import { conditionForType, conditionSchemaForType, edgeNodeOptions } from "./edgeInspectorModel";

const conditions: ConditionSchema[] = [
  {
    type: "has_value",
    config_schema: {
      type: "object",
      properties: { expected: { type: "string", default: "ready" } },
    },
    state_ports: [
      {
        name: "value",
        default_path: "scopes.{node_id}.value",
        mode: "read",
        schema: { type: "string" },
      },
    ],
  },
];

describe("edge inspector model", () => {
  test("builds condition defaults and owner-relative state bindings", () => {
    expect(conditionForType(conditions, "has_value", "source")).toEqual({
      type: "has_value",
      config: { expected: "ready" },
      state: { value: { path: "scopes.source.value" } },
    });
    expect(conditionForType(conditions, "", "source")).toBeUndefined();
  });

  test("returns only object condition schemas", () => {
    expect(conditionSchemaForType(conditions, "has_value")).toBe(conditions[0].config_schema);
    expect(conditionSchemaForType([{ type: "invalid" }], "invalid")).toBeUndefined();
  });

  test("limits virtual entry and finish edge endpoints", () => {
    const definition: GraphDefinition = { nodes: [{ id: "task", type: "task" }] };
    const virtualNodes: GraphNodeSpec[] = [
      { id: "__start__", type: "start" },
      { id: "__end__", type: "end" },
      { id: "task", type: "task" },
    ];
    const entryEdge: VirtualGraphEdge = { id: "entry", from: "__start__", to: "task", kind: "entry" };
    const finishEdge: VirtualGraphEdge = { id: "finish", from: "task", to: "__end__", kind: "finish" };

    expect(edgeNodeOptions(definition, virtualNodes, entryEdge).sourceNodes.map((node) => node.id)).toEqual(["__start__"]);
    expect(edgeNodeOptions(definition, virtualNodes, entryEdge).targetNodes.map((node) => node.id)).toEqual(["task"]);
    expect(edgeNodeOptions(definition, virtualNodes, finishEdge).sourceNodes.map((node) => node.id)).toEqual(["task"]);
    expect(edgeNodeOptions(definition, virtualNodes, finishEdge).targetNodes.map((node) => node.id)).toEqual(["__end__"]);
  });

  test("includes the virtual end node for a real graph edge", () => {
    const definition: GraphDefinition = { nodes: [{ id: "task", type: "task" }] };
    const virtualNodes: GraphNodeSpec[] = [{ id: "__end__", type: "end" }];
    expect(edgeNodeOptions(definition, virtualNodes, null).targetNodes.map((node) => node.id)).toEqual([
      "task",
      "__end__",
    ]);
  });
});
