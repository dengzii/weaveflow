import { describe, expect, test } from "bun:test";
import type {
  ConditionSchema,
  GraphDefinition,
  NodeTypeSchema,
  RegistryInfo,
  StateModuleDefinition,
} from "../types";
import {
  pendingUserInputState,
  userInputPromptFromInterrupt,
} from "../pages/workbench/userInputModel";
import { validateGraph } from "../pages/workbench/graph-workspace/utils";
import { addGraphEdge, addNodeToGraph, createGraphDefinition, createGraphID, createNodeFromType, dynamicStatePortForName, initialStateBindings, matchesDynamicStatePortName, nextDynamicStatePortName, resolvedStatePortContract } from "./graphEditor";

const modules: StateModuleDefinition[] = [
  { name: "weaveflow.protocols", version: "1" },
];

const nodeType: NodeTypeSchema = {
  type: "agent",
  state_ports: [
    { name: "task", required: true, schema: { type: "string" }, mode: "read" },
    { name: "result", schema: { type: "string" }, mode: "write" },
  ],
};

const condition: ConditionSchema = {
  type: "has_result",
  state_ports: [
    { name: "result", required: true, schema: { type: "string" }, mode: "read" },
  ],
};

const registry: RegistryInfo = {
  state_modules: modules,
  capabilities: [],
  node_groups: [],
  node_types: [nodeType],
  conditions: [condition],
  graph_schema: {},
};

describe("v2 graph editor defaults", () => {
  test("creates graph IDs without the debug prefix", () => {
    expect(createGraphID(1_234_567_890)).toBe("graph_kf12oi");
  });

  test("creates empty graph definitions without an entry point", () => {
    const graph = createGraphDefinition("empty", undefined, modules);
    expect(graph.nodes).toEqual([]);
    expect(graph.entry_point).toBeUndefined();
  });

  test("materializes declared default state paths for nodes and ports", () => {
    const schema: NodeTypeSchema = {
      type: "agent",
      state_ports: [
        { name: "task", required: true, default_path: "shared.request.input" },
        { name: "conversation", required: true, default_path: "scopes.{node_id}.conversation" },
        { name: "result", default_path: "shared.final.answer" },
      ],
    };
    expect(createNodeFromType(schema, [])).toMatchObject({
      id: "agent",
      state: {
        task: { path: "shared.request.input" },
        conversation: { path: "scopes.agent.conversation" },
        result: { path: "shared.final.answer" },
      },
    });
    expect(initialStateBindings([{ name: "conversation", default_path: "scopes.{node_id}.conversation" }], "writer.one")).toEqual({
      conversation: { path: "scopes.writer_one.conversation" },
    });
  });

  test("creates version 2 graphs with module refs and required node bindings", () => {
    const graph = createGraphDefinition("handoff", nodeType, modules);
    expect(graph.version).toBe("2.0");
    expect(graph.state_modules).toEqual([{ name: "weaveflow.protocols", version: "1" }]);
    expect(graph.nodes[0].state).toEqual({ task: { path: "" } });
  });

  test("numbers newly added nodes when their default names repeat", () => {
    const titledNodeType: NodeTypeSchema = { type: "agent", title: "Node" };
    const first = createGraphDefinition("numbered", titledNodeType, modules);
    const second = addNodeToGraph(first, titledNodeType);
    const third = addNodeToGraph(second, titledNodeType);

    expect(third.nodes.map((node) => node.name)).toEqual(["Node", "Node 1", "Node 2"]);
  });

  test("creates required condition bindings", () => {
    const graph = createGraphDefinition("handoff", nodeType, modules);
    graph.nodes.push({ id: "finish", type: "agent", state: { task: { path: "" } } });
    const next = addGraphEdge(graph, graph.nodes[0].id, "finish", condition.type, [condition]);
    expect(next.edges?.[0].condition?.state).toEqual({ result: { path: "" } });
  });

  test("supports dynamic state aliases without auto-creating hidden paths", () => {
    const dynamic = { name_pattern: "[A-Za-z_][A-Za-z0-9_]*", schema: {}, mode: "read" as const, merge_strategy: "replace" as const };
    expect(initialStateBindings(undefined, "node")).toEqual({});
    expect(nextDynamicStatePortName({ input: { path: "" } }, [{ name: "input" }], dynamic)).toBe("input_2");
    expect(matchesDynamicStatePortName("price", dynamic)).toBe(true);
    expect(matchesDynamicStatePortName("not-valid", dynamic)).toBe(false);
    expect(dynamicStatePortForName("price", dynamic)).toMatchObject({ name: "price", required: true, mode: "read" });
  });

  test("creates state operation nodes with explicit bindings and JSON null config", () => {
    const node = createNodeFromType({
      type: "state_set",
      title: "State Set",
      config_schema: {
        type: "object",
        properties: {
          value: {
            type: ["null", "boolean", "number", "string", "array", "object"],
            default: null,
            "x-control": "json",
          },
        },
        required: ["value"],
      },
      state_ports: [{
        name: "target",
        required: true,
        mode: "write",
        merge_strategy: "replace",
        schema: { title: "Any JSON value" },
      }],
    }, []);

    expect(node.config).toEqual({ value: null });
    expect(node.state).toEqual({ target: { path: "" } });
  });

  test("blocks graphs with unresolved required bindings", () => {
    const graph = createGraphDefinition("handoff", nodeType, modules);
    graph.finish_point = graph.nodes[0].id;
    expect(validateGraph(graph, registry)).toContain("requires state binding task");
    graph.nodes[0].state = { task: { path: "shared.request.input" } };
    expect(validateGraph(graph, registry)).toBe("");
  });

  test("expands capability bindings into absolute resolved contracts", () => {
    const capability = {
      id: "weaveflow.conversation.v1",
      schema: { type: "object" },
      fields: [{ name: "messages", schema: { type: "array" }, merge_strategy: "append" as const }],
    };
    const activeRegistry: RegistryInfo = {
      ...registry,
      capabilities: [capability],
    };
    const fields = resolvedStatePortContract({
      name: "conversation",
      required: true,
      capability: capability.id,
      contract: { fields: [{ path: "messages", mode: "read_write", required: true }] },
    }, { path: "scopes.writer.thread" }, activeRegistry);
    expect(fields).toEqual([{
      path: "scopes.writer.thread.messages",
      mode: "read_write",
      required: true,
      mergeStrategy: "append",
      type: "array",
    }]);
  });

  test("creates user input nodes with visible value and pending input bindings", () => {
    const userInput: NodeTypeSchema = {
      type: "user_input",
      state_ports: [
        { name: "value", schema: { type: "string" }, mode: "read_write", default_path: "shared.request.input" },
        { name: "pending_input", schema: { type: "string" }, mode: "read_write", default_path: "shared.request.pending_input" },
      ],
    };
    const graph = createGraphDefinition("interactive", userInput, modules);
    expect(graph.nodes[0].state).toEqual({
      value: { path: "shared.request.input" },
      pending_input: { path: "shared.request.pending_input" },
    });
  });

  test("recognizes bound user input only for paused runs and builds the resume patch", () => {
    const definition: GraphDefinition = {
      version: "2.0",
      state_modules: [{ name: "weaveflow.protocols", version: "1" }],
      nodes: [{
        id: "input",
        type: "user_input",
        state: {
          value: { path: "scopes.agent.input" },
          pending_input: { path: "scopes.agent.pending_input" },
        },
      }],
    };
    const interrupt = {
      run_id: "run-1",
      checkpoint_id: "checkpoint-1",
      node_id: "input",
      message: "waiting for input",
    };
    const prompt = userInputPromptFromInterrupt(interrupt, definition, {
      run_id: "run-1",
      status: "paused",
    });
    expect(prompt).toEqual({
      runID: "run-1",
      checkpointID: "checkpoint-1",
      nodeID: "input",
      statePath: "scopes.agent.pending_input",
      message: "waiting for input",
    });
    expect(pendingUserInputState(prompt?.statePath ?? "", "hello")).toEqual({
      scopes: { agent: { pending_input: "hello" } },
    });
    expect(userInputPromptFromInterrupt(interrupt, definition, {
      run_id: "run-1",
      status: "canceled",
    })).toBeNull();
    expect(userInputPromptFromInterrupt(interrupt, definition, {
      run_id: "run-2",
      status: "paused",
    })).toBeNull();
    expect(pendingUserInputState("scopes.__proto__.value", "unsafe")).toEqual({});
  });

  test("blocks user input without a pending input binding", () => {
    const userInput: NodeTypeSchema = {
      type: "user_input",
      state_ports: [
        { name: "value", schema: { type: "string" }, mode: "read_write" },
        { name: "pending_input", schema: { type: "string" }, mode: "read_write" },
      ],
    };
    const activeRegistry = { ...registry, node_types: [userInput] };
    const graph = createGraphDefinition("interactive", userInput, modules);
    graph.finish_point = graph.nodes[0].id;
    graph.nodes[0].state = { value: { path: "scopes.agent.input" } };
    expect(validateGraph(graph, activeRegistry)).toContain("requires state bindings value and pending_input");
  });
});
