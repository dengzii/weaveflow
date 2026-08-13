import { describe, expect, test } from "bun:test";
import type { GraphDefinition, RegistryInfo, RuntimeSettings, Trigger } from "../../../types";
import {
  buildGraphExportBundle,
  graphExportFilename,
  parseGraphImport,
  resolveGraphImportConflicts,
} from "./graphTransferModel";

describe("graph transfer model", () => {
  test("exports graph, config, settings, and UI as separate selected contents", () => {
    const definition = graphDefinition();
    const bundle = buildGraphExportBundle({
      definition,
      graphID: "demo/graph",
      graphVersion: "v3",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: true,
      includeSettings: true,
      includeTriggers: true,
      includeUI: true,
      exportedAt: "2026-08-05T10:00:00.000Z",
    });

    expect(bundle.contents).toEqual(["graph", "config", "settings", "triggers", "ui"]);
    expect(bundle.definition.nodes[0].config).toEqual({ prompt: "hello" });
    expect(bundle.definition.edges?.[0].condition?.config).toEqual({ expected: "ready" });
    expect(bundle.definition.metadata).toEqual({ owner: "team" });
    expect(bundle.ui?.web).toEqual({ positions: { task: { x: 20, y: 40 } } });
    expect(bundle.settings?.environment).toEqual({ WORKDIR: "/workspace" });
    expect(bundle.settings?.models?.[0].api_key).toBeUndefined();
    expect(bundle.settings?.models?.[0].pricing?.input_per_million).toBe(1.25);
    expect(bundle.settings?.tool_permissions).toEqual(["filesystem.read"]);
    expect(bundle.settings?.tool_approvals).toEqual({ bash: false });
    expect(bundle.triggers?.map((trigger) => trigger.id)).toEqual(["hook", "chat"]);
    expect(definition.metadata?.web).toBeDefined();
  });

  test("exports topology without config, settings, or UI", () => {
    const bundle = buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: false,
      includeSettings: false,
      includeTriggers: false,
      includeUI: false,
    });

    expect(bundle.contents).toEqual(["graph"]);
    expect(bundle.definition.nodes[0].config).toBeUndefined();
    expect(bundle.definition.edges?.[0].condition?.config).toBeUndefined();
    expect(bundle.definition.metadata).toEqual({ owner: "team" });
    expect(bundle.settings).toBeUndefined();
    expect(bundle.ui).toBeUndefined();
  });

  test("exports settings independently from graph config", () => {
    const bundle = buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: false,
      includeSettings: true,
      includeTriggers: false,
      includeUI: false,
    });

    expect(bundle.contents).toEqual(["graph", "settings"]);
    expect(bundle.definition.nodes[0].config).toBeUndefined();
    expect(bundle.definition.edges?.[0].condition?.config).toBeUndefined();
    expect(bundle.settings?.environment).toEqual({ WORKDIR: "/workspace" });
    expect(bundle.ui).toBeUndefined();
  });

  test("exports triggers disabled and excludes credentials and bot IDs", () => {
    const bundle = buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: true,
      includeSettings: true,
      includeTriggers: true,
      includeUI: true,
    });

    expect(bundle.settings?.environment).toEqual({ WORKDIR: "/workspace" });
    expect(bundle.settings?.models?.[0].api_key).toBeUndefined();
    expect(bundle.triggers?.[0].webhook?.api_key).toBeUndefined();
    expect(bundle.triggers?.map((trigger) => trigger.enabled)).toEqual([false, false]);
    expect(bundle.triggers?.[1].chat?.channel_config).toEqual({});
  });

  test("does not leak virtual edge config through selected UI information", () => {
    const definition = graphDefinition();
    definition.metadata = {
      web: {
        virtual_edges: [{
          id: "entry",
          from: "__start__",
          to: "task",
          kind: "entry",
          condition: { type: "state_equals", config: { expected: "ready" } },
        }],
        trigger_nodes: { hook: { x: -100, y: 20 } },
      },
    };
    const bundle = buildGraphExportBundle({
      definition,
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: false,
      includeSettings: false,
      includeTriggers: false,
      includeUI: true,
    });

    const virtualEdges = bundle.ui?.web.virtual_edges as Array<Record<string, unknown>>;
    expect(virtualEdges[0].condition).toEqual({ type: "state_equals" });
    expect(bundle.ui?.web.trigger_nodes).toBeUndefined();
  });

  test("round trips an export bundle and restores UI metadata", () => {
    const source = buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v3",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: true,
      includeSettings: true,
      includeTriggers: true,
      includeUI: true,
    });

    const imported = parseGraphImport(JSON.stringify(source));

    expect(imported.graphID).toBe("demo");
    expect(imported.graphVersion).toBe("v3");
    expect(imported.contents).toEqual(["graph", "config", "settings", "triggers", "ui"]);
    expect(imported.definition.metadata?.web).toEqual({ positions: { task: { x: 20, y: 40 } } });
    expect(imported.settings?.environment).toEqual({ WORKDIR: "/workspace" });
    expect(imported.settings?.models[0].api_key_configured).toBe(false);
    expect(imported.settings?.models[0].pricing?.output_per_million).toBe(10);
    expect(imported.settings?.tool_permissions).toEqual(["filesystem.read"]);
    expect(imported.settings?.tool_approvals).toEqual({ bash: false });
    expect(imported.triggers?.map((trigger) => trigger.target.graph_id)).toEqual(["demo", "demo"]);
    expect(imported.triggers?.map((trigger) => trigger.enabled)).toEqual([false, false]);
  });

  test("normalizes legacy runtime settings storage version during import", () => {
    const imported = parseGraphImport(JSON.stringify({
      graph_id: "legacy-demo",
      graph_version: "v1",
      definition: graphDefinition(),
      settings: {
        version: 2,
        environment: { WORKDIR: "/legacy" },
        models: [{
          id: "default",
          enabled: true,
          provider: "openai",
          model: "legacy-model",
        }],
      },
    }));

    expect(imported.settings).toEqual({
      environment: { WORKDIR: "/legacy" },
      models: [{
        id: "default",
        enabled: true,
        provider: "openai",
        api_format: "chat_completions",
        model: "legacy-model",
        base_url: "",
        extra_body: undefined,
        pricing: undefined,
        api_key_configured: false,
        api_key: undefined,
      }],
      tool_permissions: [],
      tool_approvals: {},
    });
    expect("version" in imported.settings!).toBe(false);
  });

  test("imports a plain Graph Definition JSON file", () => {
    const imported = parseGraphImport(JSON.stringify(graphDefinition()));

    expect(imported.graphID).toBe("demo");
    expect(imported.graphVersion).toBe("2.0");
    expect(imported.settings).toBeUndefined();
    expect(imported.contents).toEqual(["graph", "config", "ui"]);
  });

  test("migrates Graph Definition 1.0 nodes, conditions, and state scopes", () => {
    const imported = parseGraphImport(JSON.stringify({
      version: "1.0",
      name: "legacy",
      state_schema: "weaveflow.state.v2",
      entry_point: "input",
      finish_point: "agent",
      nodes: [
        { id: "input", type: "human_message", config: { state_scope: "worker", content: "hello" } },
        { id: "model", type: "llm", config: { state_scope: "worker", model_id: "default" } },
        { id: "tools", type: "tools", config: { state_scope: "worker", parallel: true } },
        {
          id: "agent",
          type: "agent",
          config: {
            state_scope: "researcher",
            input_path: "shared.custom.task",
            output_path: "shared.custom.answer",
            tool_name: "legacy-agent",
          },
        },
      ],
      edges: [
        { from: "input", to: "model" },
        {
          from: "model",
          to: "tools",
          condition: { type: "last_message_has_tool_calls", config: { state_scope: "worker" } },
        },
        { from: "tools", to: "agent" },
      ],
    }), migrationRegistry());

    expect(imported.definition.version).toBe("2.0");
    expect(imported.definition.state_modules).toEqual([{ name: "weaveflow.protocols", version: "1" }]);
    expect(imported.definition.nodes[0]).toMatchObject({
      id: "input",
      type: "conversation_message",
      config: { role: "human", content: "hello" },
      state: { conversation: { path: "scopes.worker.conversation" } },
    });
    expect(imported.definition.nodes[1]).toMatchObject({
      type: "llm_turn",
      state: { conversation: { path: "scopes.worker.conversation" } },
    });
    expect(imported.definition.nodes[1].state?.output).toBeUndefined();
    expect(imported.definition.nodes[2]).toMatchObject({
      type: "tool_execution",
      state: { conversation: { path: "scopes.worker.conversation" } },
    });
    expect(imported.definition.nodes[3]).toMatchObject({
      type: "agent",
      state: {
        task: { path: "shared.custom.task" },
        conversation: { path: "scopes.researcher.conversation" },
        result: { path: "shared.custom.answer" },
      },
    });
    expect(imported.definition.nodes[3].config?.tool_name).toBeUndefined();
    expect(imported.definition.edges?.[1].condition).toEqual({
      type: "conversation_has_tool_calls",
      state: { conversation: { path: "scopes.worker.conversation" } },
    });
    expect(imported.migration).toMatchObject({ sourceVersion: "1.0", targetVersion: "2.0" });
    expect(imported.migration?.warnings).toContain("Node \"model\" type migrated from llm to llm_turn.");
    expect(imported.migration?.warnings).toContain("Node \"agent\" dropped obsolete agent tool_name/tool_description config.");
  });

  test("uses the legacy default agent scope during migration", () => {
    const imported = parseGraphImport(JSON.stringify({
      version: "1.0",
      nodes: [{ id: "model", type: "llm" }],
    }), migrationRegistry());

    expect(imported.definition.nodes[0].state).toEqual({
      conversation: { path: "scopes.agent.conversation" },
    });
  });

  test("migrates legacy expression conditions relative to their state scope", () => {
    const imported = parseGraphImport(JSON.stringify({
      version: "1.0",
      nodes: [{ id: "model", type: "llm" }],
      edges: [{
        from: "model",
        to: "__end__",
        condition: {
          type: "expression_conditions",
          config: {
            state_scope: "worker",
            match: "all",
            expressions: [{ value1: "final_answer", op: "not_equal", value2: "" }],
          },
        },
      }],
    }), migrationRegistry());

    expect(imported.definition.edges?.[0].condition).toEqual({
      type: "expression_conditions",
      config: {
        match: "all",
        expressions: [{ value1: "conversation.final_answer", op: "not_equal", value2: "" }],
      },
      state: { state: { path: "scopes.worker" } },
    });
  });

  test("rejects legacy graphs that cannot be migrated without changing semantics", () => {
    expect(() => parseGraphImport(JSON.stringify({
      version: "1.0",
      state_schema: "custom.state.v1",
      nodes: [{ id: "model", type: "llm" }],
    }), migrationRegistry())).toThrow("state_schema");
    expect(() => parseGraphImport(JSON.stringify({
      version: "1.0",
      nodes: [{ id: "input", type: "human_message", config: {} }],
    }), migrationRegistry())).toThrow("requires a user_input plus conversation_message topology");
    expect(() => parseGraphImport(JSON.stringify({
      version: "3.0",
      nodes: [{ id: "model", type: "llm" }],
    }), migrationRegistry())).toThrow("Unsupported Graph Definition version");
  });

  test("generates a new graph ID and numbered name for import conflicts", () => {
    const imported = parseGraphImport(JSON.stringify(buildGraphExportBundle({
      definition: {
        ...graphDefinition(),
        metadata: {
          web: {
            trigger_nodes: {
              hook: { x: -100, y: 20 },
              chat: { x: -100, y: 120 },
            },
          },
        },
      },
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: true,
      includeSettings: false,
      includeTriggers: true,
      includeUI: true,
    })));
    const resolved = resolveGraphImportConflicts(
      imported,
      ["demo", "graph_clone"],
      ["demo", "demo 1"],
      ["hook", "chat"],
      "graph_clone"
    );

    expect(resolved.graphID).toBe("graph_clone_1");
    expect(resolved.definition.name).toBe("demo 2");
    expect(resolved.triggers?.map((trigger) => trigger.id)).toEqual(["hook_1", "chat_1"]);
    expect(resolved.triggers?.map((trigger) => trigger.target.graph_id)).toEqual([
      "graph_clone_1",
      "graph_clone_1",
    ]);
    expect(resolved.definition.metadata?.web).toEqual({
      trigger_nodes: {
        hook_1: { x: -100, y: 20 },
        chat_1: { x: -100, y: 120 },
      },
    });
    expect(imported.graphID).toBe("demo");
    expect(imported.definition.name).toBe("demo");
  });

  test("always assigns imports a new graph ID to isolate them from stored sessions", () => {
    const imported = parseGraphImport(JSON.stringify(buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "legacy-demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: true,
      includeSettings: true,
      includeTriggers: true,
      includeUI: false,
    })));

    const resolved = resolveGraphImportConflicts(
      imported,
      ["another-graph"],
      ["another graph"],
      [],
      "graph_imported"
    );

    expect(resolved.graphID).toBe("graph_imported");
    expect(resolved.triggers?.[0].target.graph_id).toBe("graph_imported");
    expect(imported.graphID).toBe("legacy-demo");
  });

  test("rejects malformed files and sanitizes download filenames", () => {
    expect(() => parseGraphImport("{bad json")).toThrow("Invalid JSON");
    expect(() => parseGraphImport(JSON.stringify({ format: "other", nodes: [] }))).toThrow(
      "Unsupported graph export format"
    );
    expect(graphExportFilename("team/demo:graph", graphDefinition())).toBe("team-demo-graph.weaveflow.json");
  });
});

function graphDefinition(): GraphDefinition {
  return {
    version: "2.0",
    name: "demo",
    state_modules: [{ name: "weaveflow.protocols", version: "1" }],
    entry_point: "task",
    finish_point: "task",
    nodes: [{
      id: "task",
      type: "task",
      config: { prompt: "hello" },
      state: { output: { path: "shared.output" } },
    }],
    edges: [{
      from: "task",
      to: "__end__",
      condition: { type: "state_equals", config: { expected: "ready" } },
    }],
    metadata: {
      owner: "team",
      web: { positions: { task: { x: 20, y: 40 } } },
    },
  };
}

function graphTriggers(): Trigger[] {
  return [
    {
      id: "hook",
      name: "Webhook",
      type: "webhook",
      enabled: true,
      concurrency: "parallel",
      target: { graph_id: "demo" },
      webhook: {
        api_key: "webhook-secret",
        state_mappings: [{ parameter: "user.id", state_path: "shared.user.id" }],
      },
      created_at: "",
      updated_at: "",
    },
    {
      id: "chat",
      name: "Chat",
      type: "chat",
      enabled: true,
      concurrency: "skip",
      target: { graph_id: "demo" },
      chat: {
        channel: "wecom",
        channel_config: { bot_id: "bot", secret: "chat-secret" },
        stream_updates: true,
      },
      created_at: "",
      updated_at: "",
    },
  ];
}

function runtimeSettings(): RuntimeSettings {
  return {
    environment: {
      WORKDIR: "/workspace",
      SERVICE_TOKEN: "secret-value",
    },
    models: [{
      id: "default",
      enabled: true,
      provider: "openai",
      model: "gpt-5",
      base_url: "https://api.example.test/v1",
      api_key_configured: true,
      api_key: "local-secret",
      pricing: {
        currency: "USD",
        input_per_million: 1.25,
        cached_input_per_million: 0.25,
        output_per_million: 10,
      },
    }],
    tool_permissions: ["filesystem.read"],
    tool_approvals: { bash: false },
  };
}

function migrationRegistry(): RegistryInfo {
  const conversationPort = {
    name: "conversation",
    required: true,
    capability: "weaveflow.conversation.v1",
  };
  return {
    state_modules: [{
      name: "weaveflow.protocols",
      version: "1",
      fields: [],
      capabilities: [],
    }],
    capabilities: [],
    node_groups: [],
    node_types: [
      {
        type: "conversation_message",
        state_ports: [
          { name: "input", default_path: "shared.request.input", mode: "read" },
          conversationPort,
        ],
      },
      {
        type: "llm_turn",
        state_ports: [
          conversationPort,
          { name: "output", default_path: "shared.final.answer", mode: "write" },
        ],
      },
      { type: "tool_execution", state_ports: [conversationPort] },
      {
        type: "agent",
        state_ports: [
          { name: "task", required: true, default_path: "shared.request.input", mode: "read" },
          conversationPort,
          { name: "result", required: true, default_path: "shared.final.answer", mode: "write" },
        ],
      },
      { type: "context_reducer", state_ports: [conversationPort] },
      {
        type: "environment_context",
        state_ports: [{ name: "environment", required: true, default_path: "shared.environment", mode: "write" }],
      },
    ],
    conditions: [
      { type: "conversation_has_tool_calls", state_ports: [conversationPort] },
      { type: "conversation_has_final_answer", state_ports: [conversationPort] },
      {
        type: "expression_conditions",
        state_ports: [{ name: "state", required: true, mode: "read" }],
      },
    ],
    graph_schema: {},
  };
}
