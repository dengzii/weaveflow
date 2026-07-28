import { describe, expect, test } from "bun:test";
import type { GraphDefinition, RegistryInfo } from "../../../types";
import { buildGraphLintIssues } from "./lint";

const validSupervisorGraph: GraphDefinition = {
  version: "2.0",
  name: "supervisor",
  state_modules: [{ name: "weaveflow.protocols", version: "1" }],
  entry_point: "supervisor",
  finish_point: "synthesis",
  nodes: [
    {
      id: "supervisor",
      type: "supervisor",
      config: { members: [{ id: "researcher", description: "Find facts." }] },
    },
    {
      id: "research_node",
      type: "supervisor_worker",
      config: { worker_id: "researcher" },
    },
    { id: "synthesis", type: "supervisor_synthesis", config: {} },
  ],
  edges: [
    {
      from: "supervisor",
      to: "research_node",
      condition: { type: "supervisor_route_equals", config: { worker_id: "researcher" } },
    },
    { from: "supervisor", to: "synthesis" },
    { from: "research_node", to: "supervisor" },
  ],
};

describe("supervisor graph lint", () => {
  test("accepts a complete supervisor loop", () => {
    const issues = buildGraphLintIssues({
      definition: validSupervisorGraph,
      initialStateText: "{}",
      initialRequirements: null,
    });
    expect(issues.filter((issue) => issue.id.startsWith("supervisor-"))).toEqual([]);
  });

  test("reports missing worker routes, return edges, and synthesis fallback", () => {
    const broken: GraphDefinition = {
      ...validSupervisorGraph,
      nodes: validSupervisorGraph.nodes.map((node) => ({ ...node, config: { ...(node.config ?? {}) } })),
      edges: [],
    };
    const issues = buildGraphLintIssues({
      definition: broken,
      initialStateText: "{}",
      initialRequirements: null,
    });
    const ids = issues.map((issue) => issue.id);
    expect(ids).toContain("supervisor-route-missing-supervisor-researcher");
    expect(ids).toContain("supervisor-return-missing-supervisor-researcher");
    expect(ids).toContain("supervisor-synthesis-missing-supervisor");
  });
});

const conversationCapability = {
  id: "weaveflow.conversation.v1",
  schema: { type: "object" },
  fields: [
    { name: "messages", schema: { type: "array" }, merge_strategy: "append" as const },
  ],
};

const planCapability = {
  id: "weaveflow.plan.v1",
  schema: { type: "object" },
  fields: [
    { name: "status", schema: { type: "string" }, merge_strategy: "replace" as const },
  ],
};

const registry: RegistryInfo = {
  state_modules: [
    {
      name: "weaveflow.protocols",
      version: "1",
      fields: [{ path: "shared.request.input", schema: { type: "string" } }],
      capabilities: [conversationCapability],
    },
    {
      name: "weaveflow.planning",
      version: "1",
      capabilities: [planCapability],
    },
  ],
  capabilities: [conversationCapability, planCapability],
  node_groups: [],
  node_types: [
    {
      type: "writer",
      state_ports: [
        { name: "output", required: true, mode: "write", schema: { type: "string" } },
        {
          name: "conversation",
          required: true,
          capability: conversationCapability.id,
          contract: { fields: [{ path: "messages", mode: "write" }] },
        },
      ],
    },
    {
      type: "consumer",
      state_ports: [
        { name: "input", required: true, mode: "read", schema: { type: "string" } },
        {
          name: "conversation",
          required: true,
          capability: conversationCapability.id,
          contract: { fields: [{ path: "messages", mode: "read" }] },
        },
      ],
    },
  ],
  conditions: [
    {
      type: "has_messages",
      state_ports: [
        {
          name: "conversation",
          required: true,
          capability: conversationCapability.id,
          contract: { fields: [{ path: "messages", mode: "read" }] },
        },
      ],
    },
  ],
  graph_schema: {},
};

function bindingRegistry(): RegistryInfo {
  return JSON.parse(JSON.stringify(registry)) as RegistryInfo;
}

function bindingGraph(): GraphDefinition {
  return {
    version: "2.0",
    name: "bindings",
    state_modules: [{ name: "weaveflow.protocols", version: "1" }],
    entry_point: "writer",
    finish_point: "consumer",
    nodes: [
      {
        id: "writer",
        type: "writer",
        config: { asset_path: "not.a.state.path" },
        state: {
          output: { path: "shared.handoff" },
          conversation: { path: "scopes.writer.conversation" },
        },
      },
      {
        id: "consumer",
        type: "consumer",
        state: {
          input: { path: "shared.handoff" },
          conversation: { path: "scopes.writer.conversation" },
        },
      },
    ],
    edges: [
      {
        from: "writer",
        to: "consumer",
        condition: {
          type: "has_messages",
          state: { conversation: { path: "scopes.writer.conversation" } },
        },
      },
    ],
  };
}

describe("state binding lint", () => {
  test("accepts compatible node and condition bindings", () => {
    const activeRegistry = bindingRegistry();
    const issues = buildGraphLintIssues({
      definition: bindingGraph(),
      initialStateText: "{}",
      initialRequirements: null,
      registry: activeRegistry,
    });
    expect(issues.filter((issue) => issue.id.includes("binding"))).toEqual([]);
    expect(issues.some((issue) => issue.message.includes("asset_path"))).toBe(false);
  });

  test("accepts dynamic state aliases and enforces dynamic counts", () => {
    const activeRegistry = bindingRegistry();
    activeRegistry.node_types[0] = {
      ...activeRegistry.node_types[0],
      dynamic_state_ports: {
        name_pattern: "[A-Za-z_][A-Za-z0-9_]*",
        min_ports: 2,
        max_ports: 3,
        schema: {},
        mode: "read",
        merge_strategy: "replace",
      },
    };
    const graph = bindingGraph();
    graph.nodes[0] = {
      ...graph.nodes[0],
      state: {
        ...graph.nodes[0].state,
        price: { path: "shared.price" },
        quantity: { path: "shared.quantity" },
      },
    };
    const accepted = buildGraphLintIssues({ definition: graph, initialStateText: "{}", initialRequirements: null, registry: activeRegistry });
    expect(accepted.some((issue) => issue.id.includes("binding-unknown-price"))).toBe(false);
    expect(accepted.some((issue) => issue.id.includes("binding-dynamic-min"))).toBe(false);

    graph.nodes[0].state = { ...graph.nodes[0].state, "not-valid": { path: "shared.bad" } };
    const rejected = buildGraphLintIssues({ definition: graph, initialStateText: "{}", initialRequirements: null, registry: activeRegistry });
    expect(rejected.some((issue) => issue.id.includes("binding-unknown-not-valid"))).toBe(true);
  });

  test("rejects missing, unknown, reserved, and primitive-conflicting bindings", () => {
    const activeRegistry = bindingRegistry();
    const graph = bindingGraph();
    graph.nodes[1] = {
      ...graph.nodes[1],
      state: {
        conversation: { path: "runtime.conversation" },
        extra: { path: "shared.extra" },
        input: { path: "shared.request.input" },
      },
    };
    activeRegistry.node_types[1] = {
      ...activeRegistry.node_types[1],
      state_ports: activeRegistry.node_types[1].state_ports?.map((port) =>
        port.name === "input" ? { ...port, schema: { type: "number" } } : port
      ),
    };
    const issues = buildGraphLintIssues({
      definition: graph,
      initialStateText: "{}",
      initialRequirements: null,
      registry: activeRegistry,
    });
    expect(issues.some((issue) => issue.id.includes("binding-unknown-extra"))).toBe(true);
    expect(issues.some((issue) => issue.message.includes("reserved"))).toBe(true);
    expect(issues.some((issue) => issue.message.includes("conflicts with module field"))).toBe(true);
  });

  test("rejects missing required bindings and unreferenced capabilities", () => {
    const activeRegistry = bindingRegistry();
    const graph = bindingGraph();
    graph.nodes[1] = {
      ...graph.nodes[1],
      state: {
        input: { path: "shared.handoff" },
        plan: { path: "shared.plan" },
      },
    };
    activeRegistry.node_types[1] = {
      ...activeRegistry.node_types[1],
      state_ports: [
        ...(activeRegistry.node_types[1].state_ports ?? []),
        {
          name: "plan",
          required: true,
          capability: planCapability.id,
          contract: { fields: [{ path: "status", mode: "read" }] },
        },
      ],
    };
    const issues = buildGraphLintIssues({
      definition: graph,
      initialStateText: "{}",
      initialRequirements: null,
      registry: activeRegistry,
    });
    expect(issues.some((issue) => issue.id.includes("binding-required-conversation"))).toBe(true);
    expect(issues.some((issue) => issue.message.includes("unreferenced state module"))).toBe(true);
  });

  test("rejects expanded capability fields that conflict with module schemas", () => {
    const activeRegistry = bindingRegistry();
    activeRegistry.state_modules[0].fields?.push({
      path: "scopes.writer.conversation.messages",
      schema: { type: "string" },
    });
    const issues = buildGraphLintIssues({
      definition: bindingGraph(),
      initialStateText: "{}",
      initialRequirements: null,
      registry: activeRegistry,
    });
    expect(issues.some((issue) => issue.message.includes('capability field "scopes.writer.conversation.messages" conflicts'))).toBe(true);
  });

  test("surfaces backend producer and parallel contract failures as blocking lint", () => {
    const issues = buildGraphLintIssues({
      definition: bindingGraph(),
      initialStateText: "{}",
      initialRequirements: null,
      analysisError: 'graph contract validation failed:\n- parallel branches "left" and "right" both write overlapping path "shared.answer"',
      registry: bindingRegistry(),
    });
    expect(issues).toContainEqual(expect.objectContaining({
      id: "graph-contract-analysis-error",
      severity: "error",
    }));
  });
});
