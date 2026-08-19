import { describe, expect, test } from "bun:test";
import type { GraphDefinition, RuntimeSettings, Trigger } from "../../../types";
import {
  buildGraphExportBundle,
  graphExportFilename,
  importedModelIDsRequiringCredentials,
  parseGraphImport,
  prepareImportedRuntimeSettings,
  resolveGraphImport,
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
    expect(bundle.settings?.models?.[0]).not.toHaveProperty("credential_configured");
    expect(bundle.settings?.environment_secrets).toEqual({ SERVICE_TOKEN: { source: "env", ref: "SERVICE_TOKEN" } });
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

  test("omits server-managed credentials and pending values from exports", () => {
    const settings = runtimeSettings();
    settings.models[0].credential_value = "pending-secret";
    settings.environment_secrets.LOCAL_TOKEN = { source: "managed", ref: "00000000-0000-4000-8000-000000000002" };
    const triggers = graphTriggers();
    triggers[0].credential = { source: "managed", ref: "00000000-0000-4000-8000-000000000003" };
    const bundle = buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: settings,
      triggers,
      includeConfig: false,
      includeSettings: true,
      includeTriggers: true,
      includeUI: false,
    });

    expect(bundle.settings?.models?.[0]).not.toHaveProperty("credential_configured");
    expect(bundle.settings?.environment_secrets?.LOCAL_TOKEN).toBeUndefined();
    expect(bundle.triggers?.[0].credential).toBeUndefined();
    expect(JSON.stringify(bundle)).not.toContain("pending-secret");
    expect(JSON.stringify(bundle)).not.toContain("00000000-0000-4000-8000-000000000003");
  });

  test("exports triggers disabled with credential refs and without credential values or bot IDs", () => {
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
    expect(bundle.triggers?.[0].credential).toEqual({ source: "env", ref: "TRIGGER_TOKEN" });
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
    expect(imported.settings?.models[0].credential_configured).toBe(false);
    expect(imported.settings?.models[0].pricing?.output_per_million).toBe(10);
    expect(imported.settings?.tool_permissions).toEqual(["filesystem.read"]);
    expect(imported.settings?.tool_approvals).toEqual({ bash: false });
    expect(imported.triggers?.[0].credential).toEqual({ source: "env", ref: "TRIGGER_TOKEN" });
    expect(imported.triggers?.map((trigger) => trigger.target.graph_id)).toEqual(["demo", "demo"]);
    expect(imported.triggers?.map((trigger) => trigger.enabled)).toEqual([false, false]);
  });

  test("prepares imported models with explicit or reusable credentials", () => {
    const imported = parseGraphImport(JSON.stringify(buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: [],
      includeConfig: true,
      includeSettings: true,
      includeTriggers: false,
      includeUI: false,
    })));
    const settings = imported.settings!;

    expect(importedModelIDsRequiringCredentials(settings, undefined)).toEqual(["default"]);
    expect(prepareImportedRuntimeSettings(settings, undefined, {}).models[0]).toMatchObject({
      id: "default",
      enabled: false,
      credential_configured: false,
    });
    expect(prepareImportedRuntimeSettings(settings, undefined, { default: " imported-key " }).models[0]).toMatchObject({
      id: "default",
      enabled: true,
      credential_configured: true,
      credential_value: "imported-key",
    });

    const reusable = runtimeSettings();
    reusable.models[0].base_url = "https://api.example.test/v1/";
    reusable.models[0].credential_value = "pending-reusable-key";
    expect(importedModelIDsRequiringCredentials(settings, reusable)).toEqual([]);
    expect(prepareImportedRuntimeSettings(settings, reusable, {}).models[0]).toMatchObject({
      id: "default",
      enabled: true,
      credential_configured: true,
      credential_value: "pending-reusable-key",
    });
  });

  test("does not reuse a managed credential for a different endpoint", () => {
    const settings = runtimeSettings();
    settings.models[0].credential_configured = false;
    const reusable = runtimeSettings();
    reusable.models[0].base_url = "https://other.example.test/v1";

    expect(importedModelIDsRequiringCredentials(settings, reusable)).toEqual(["default"]);
    expect(prepareImportedRuntimeSettings(settings, reusable, {}).models[0].enabled).toBe(false);
  });

  test("rejects removed plaintext webhook credentials during import", () => {
    expect(() => parseGraphImport(JSON.stringify({
      format: "weaveflow.graph-export",
      format_version: "1.0",
      exported_at: "2026-08-17T00:00:00Z",
      contents: ["graph", "config", "triggers"],
      graph_id: "demo",
      graph_version: "v1",
      definition: { ...graphDefinition(), metadata: undefined },
      triggers: [{
        id: "hook",
        type: "webhook",
        enabled: false,
        webhook: { api_key: "plaintext" },
      }],
    }))).toThrow("removed plaintext webhook api_key");
  });

  test("rejects legacy export envelopes without the explicit format contract", () => {
    expect(() => parseGraphImport(JSON.stringify({
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
    }))).toThrow("Unsupported graph export format: missing");
  });

  test("rejects plain Graph Definition files without an export contract", () => {
    expect(() => parseGraphImport(JSON.stringify(graphDefinition())))
      .toThrow("Unsupported graph export format: missing");
  });

  test("rejects removed and unsupported Graph Definition formats", () => {
    expect(() => parseGraphImport(JSON.stringify({
      version: "1.0",
      nodes: [{ id: "model", type: "llm" }],
    }))).toThrow("Unsupported graph export format: missing");
    expect(() => parseGraphImport(JSON.stringify({
      version: "2.0",
      state_modules: [{ name: "weaveflow.protocols", version: "1" }],
      nodes: [{ id: "model", type: "llm" }],
    }))).toThrow("Unsupported graph export format: missing");
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
    const resolved = resolveGraphImport(imported, {
      strategy: "copy",
      existingGraphIDs: ["demo", "graph_clone"],
      existingGraphNames: ["demo", "demo 1"],
      existingTriggerIDs: ["hook", "chat"],
      generatedGraphID: "graph_clone",
    });

    expect(resolved.graphID).toBe("graph_clone_1");
    expect(resolved.definition.name).toBe("demo 2");
    expect(resolved.triggers?.map((trigger) => trigger.id)).toEqual(["hook", "chat"]);
    expect(resolved.triggers?.map((trigger) => trigger.target.graph_id)).toEqual([
      "graph_clone_1",
      "graph_clone_1",
    ]);
    expect(resolved.definition.metadata?.web).toEqual({
      trigger_nodes: {
        hook: { x: -100, y: 20 },
        chat: { x: -100, y: 120 },
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

    const resolved = resolveGraphImport(imported, {
      strategy: "copy",
      existingGraphIDs: ["another-graph"],
      existingGraphNames: ["another graph"],
      generatedGraphID: "graph_imported",
    });

    expect(resolved.graphID).toBe("graph_imported");
    expect(resolved.triggers?.[0].target.graph_id).toBe("graph_imported");
    expect(imported.graphID).toBe("legacy-demo");
  });

  test("overwrites a graph without changing graph or trigger IDs", () => {
    const imported = parseGraphImport(JSON.stringify(buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v4",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: true,
      includeSettings: true,
      includeTriggers: true,
      includeUI: true,
    })));

    const resolved = resolveGraphImport(imported, {
      strategy: "overwrite",
      existingGraphIDs: ["demo"],
      existingGraphNames: ["demo"],
      existingTriggerIDs: ["hook", "chat"],
    });

    expect(resolved.graphID).toBe("demo");
    expect(resolved.definition.name).toBe("demo");
    expect(resolved.triggers?.map((trigger) => trigger.id)).toEqual(["hook", "chat"]);
    expect(resolved.triggers?.map((trigger) => trigger.target.graph_id)).toEqual(["demo", "demo"]);
  });

  test("preserves the target trigger list when an overwrite import omits triggers", () => {
    const imported = parseGraphImport(JSON.stringify(buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: true,
      includeSettings: false,
      includeTriggers: false,
      includeUI: false,
    })));

    const resolved = resolveGraphImport(imported, {
      strategy: "overwrite",
      existingGraphIDs: ["demo"],
      existingGraphNames: ["demo"],
    });

    expect(resolved.triggers).toBeUndefined();
  });

  test("rejects contents that disagree with exported fields", () => {
    const source = buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      includeConfig: true,
      includeSettings: true,
      includeTriggers: false,
      includeUI: false,
    });
    delete source.settings;
    expect(() => parseGraphImport(JSON.stringify(source))).toThrow("contents declares settings");
    source.contents = ["graph", "config"];
    source.settings = {};
    expect(() => parseGraphImport(JSON.stringify(source))).toThrow("settings is present");
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
    version: "1.0",
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
      credential: { source: "env", ref: "TRIGGER_TOKEN" },
      webhook: {
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
    environment_secrets: {
      SERVICE_TOKEN: { source: "env", ref: "SERVICE_TOKEN" },
    },
    models: [{
      id: "default",
      enabled: true,
      provider: "openai",
      model: "gpt-5",
      base_url: "https://api.example.test/v1",
      credential_configured: true,
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
