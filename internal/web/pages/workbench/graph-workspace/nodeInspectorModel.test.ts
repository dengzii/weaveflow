import { describe, expect, test } from "bun:test";
import type { GraphDefinition, GraphNodeSpec, NodeTypeSchema, StepRecord } from "../../../types";
import {
  analyzeNodeDetails,
  configSchemaFields,
  nodeTypeForType,
  schemaForNodeType,
} from "./nodeInspectorModel";

const nodeType: NodeTypeSchema = {
  type: "worker",
  title: "Worker",
  config_schema: {
    type: "object",
    properties: {
      retries: { type: "integer" },
      prompt: { type: "string" },
    },
    required: ["prompt"],
  },
};

describe("node inspector model", () => {
  test("matches normalized node types and exposes valid config schemas", () => {
    expect(nodeTypeForType([nodeType], " worker ")).toBe(nodeType);
    expect(schemaForNodeType([nodeType], "worker")).toBe(nodeType.config_schema);
    expect(schemaForNodeType([{ type: "invalid", config_schema: undefined }], "invalid")).toBeUndefined();
  });

  test("sorts schema fields and marks required entries", () => {
    expect(configSchemaFields(nodeType.config_schema)).toEqual(["prompt *", "retries"]);
  });

  test("summarizes graph placement, roles, configuration, and latest runtime step", () => {
    const node: GraphNodeSpec = { id: "worker", type: "worker", config: { retries: 2, prompt: "Draft" } };
    const details = analyzeNodeDetails(
      graphDefinition(node),
      node,
      nodeType,
      node.config ?? {},
      nodeType.config_schema,
      steps(),
      true
    );

    expect(details.indexLabel).toBe("1 of 2");
    expect(details.positionLabel).toBe("13, 21");
    expect(details.roles).toEqual(["entry", "finish", "end edge"]);
    expect(details.configKeys).toEqual(["prompt", "retries"]);
    expect(details.schemaFields).toEqual(["prompt *", "retries"]);
    expect(details.typeLabel).toBe("Worker (worker)");
    expect(details.latestStep?.step_id).toBe("newer");
    expect(details.incoming).toHaveLength(1);
    expect(details.outgoing).toHaveLength(1);
  });

  test("distinguishes unavailable and loaded registries for unknown node types", () => {
    const node: GraphNodeSpec = { id: "unknown", type: "custom" };
    expect(analyzeNodeDetails(null, node, undefined, {}, undefined, [], false).typeLabel).toContain("registry unavailable");
    expect(analyzeNodeDetails(null, node, undefined, {}, undefined, [], true).typeLabel).toContain("unregistered");
  });
});

function graphDefinition(node: GraphNodeSpec): GraphDefinition {
  return {
    version: "2.0",
    entry_point: node.id,
    finish_point: node.id,
    nodes: [node, { id: "source", type: "source" }],
    edges: [
      { from: "source", to: node.id },
      { from: node.id, to: "__end__" },
    ],
    metadata: {
      web: {
        positions: {
          [node.id]: { x: 12.7, y: 20.6 },
        },
      },
    },
  };
}

function steps(): StepRecord[] {
  return [
    {
      step_id: "older",
      run_id: "run",
      node_id: "worker",
      node_name: "Worker",
      status: "completed",
      attempt: 1,
      started_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:01Z",
    },
    {
      step_id: "newer",
      run_id: "run",
      node_id: "worker",
      node_name: "Worker",
      status: "failed",
      attempt: 2,
      started_at: "2026-01-01T00:00:02Z",
      updated_at: "2026-01-01T00:00:03Z",
    },
    {
      step_id: "other-node",
      run_id: "run",
      node_id: "source",
      node_name: "Source",
      status: "completed",
      attempt: 1,
      started_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:04Z",
    },
  ];
}
