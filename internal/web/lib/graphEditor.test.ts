import { describe, expect, test } from "bun:test";
import type {
  ConditionSchema,
  GraphDefinition,
  NodeTypeSchema,
  RegistryInfo,
  StateModuleDefinition,
} from "../types";
import {
  humanMessagePromptFromInterrupt,
  pendingHumanInputState,
} from "../pages/WorkbenchPage";
import { validateGraph } from "../pages/workbench/graph-workspace/utils";
import { addGraphEdge, addNodeToGraph, createGraphDefinition, createNodeFromType, initialStateBindings, resolvedStatePortContract } from "./graphEditor";

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
  node_types: [nodeType],
  conditions: [condition],
  graph_schema: {},
};

describe("v2 graph editor defaults", () => {
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

  test("creates conversation input nodes with a visible pending input binding", () => {
    const conversationInput: NodeTypeSchema = {
      type: "conversation_input",
      state_ports: [
        { name: "conversation", required: true, capability: "weaveflow.conversation.v1", default_path: "scopes.{node_id}.conversation" },
        { name: "pending_input", schema: { type: "string" }, mode: "read_write", default_path: "shared.request.pending_input" },
      ],
    };
    const graph = createGraphDefinition("interactive", conversationInput, modules);
    expect(graph.nodes[0].state).toEqual({
      conversation: { path: "scopes.conversation_input.conversation" },
      pending_input: { path: "shared.request.pending_input" },
    });
  });

  test("recognizes bound conversation input pauses and builds the resume patch", () => {
    const definition: GraphDefinition = {
      version: "2.0",
      state_modules: [{ name: "weaveflow.protocols", version: "1" }],
      nodes: [{
        id: "input",
        type: "conversation_input",
        state: {
          conversation: { path: "scopes.agent.conversation" },
          pending_input: { path: "scopes.agent.pending_input" },
        },
      }],
    };
    const prompt = humanMessagePromptFromInterrupt({
      run_id: "run-1",
      checkpoint_id: "checkpoint-1",
      node_id: "input",
      message: "waiting for input",
    }, definition);
    expect(prompt).toEqual({
      runId: "run-1",
      checkpointId: "checkpoint-1",
      nodeId: "input",
      statePath: "scopes.agent.pending_input",
      message: "waiting for input",
    });
    expect(pendingHumanInputState(prompt?.statePath ?? "", "hello")).toEqual({
      scopes: { agent: { pending_input: "hello" } },
    });
  });

  test("blocks interactive conversation input without a pending input binding", () => {
    const conversationInput: NodeTypeSchema = {
      type: "conversation_input",
      state_ports: [
        { name: "conversation", required: true, capability: "weaveflow.conversation.v1" },
        { name: "input", schema: { type: "string" }, mode: "read" },
        { name: "pending_input", schema: { type: "string" }, mode: "read_write" },
      ],
    };
    const activeRegistry = { ...registry, node_types: [conversationInput] };
    const graph = createGraphDefinition("interactive", conversationInput, modules);
    graph.finish_point = graph.nodes[0].id;
    graph.nodes[0].state = { conversation: { path: "scopes.agent.conversation" } };
    expect(validateGraph(graph, activeRegistry)).toContain("requires state binding pending_input");
  });
});
