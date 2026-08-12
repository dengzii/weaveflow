import { describe, expect, test } from "bun:test";
import type { GraphDefinition, RuntimeSettings, Trigger } from "../../../types";
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
    expect(imported.triggers?.map((trigger) => trigger.target.graph_id)).toEqual(["demo", "demo"]);
    expect(imported.triggers?.map((trigger) => trigger.enabled)).toEqual([false, false]);
  });

  test("imports a plain Graph Definition JSON file", () => {
    const imported = parseGraphImport(JSON.stringify(graphDefinition()));

    expect(imported.graphID).toBe("demo");
    expect(imported.graphVersion).toBe("2.0");
    expect(imported.settings).toBeUndefined();
    expect(imported.contents).toEqual(["graph", "config", "ui"]);
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
    }],
  };
}
