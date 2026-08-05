import { describe, expect, test } from "bun:test";
import type { GraphDefinition, RuntimeSettings } from "../../../types";
import {
  buildGraphExportBundle,
  graphExportFilename,
  parseGraphImport,
} from "./graphTransferModel";

describe("graph transfer model", () => {
  test("exports graph, config, settings, and UI as separate selected contents", () => {
    const definition = graphDefinition();
    const bundle = buildGraphExportBundle({
      definition,
      graphID: "demo/graph",
      graphVersion: "v3",
      runtimeSettings: runtimeSettings(),
      includeConfig: true,
      includeSettings: true,
      includeUI: true,
      exportedAt: "2026-08-05T10:00:00.000Z",
    });

    expect(bundle.contents).toEqual(["graph", "config", "settings", "ui"]);
    expect(bundle.definition.nodes[0].config).toEqual({ prompt: "hello" });
    expect(bundle.definition.edges?.[0].condition?.config).toEqual({ expected: "ready" });
    expect(bundle.definition.metadata).toEqual({ owner: "team" });
    expect(bundle.ui?.web).toEqual({ positions: { task: { x: 20, y: 40 } } });
    expect(bundle.settings?.environment).toEqual({ WORKDIR: "/workspace" });
    expect(bundle.settings?.models?.[0].api_key).toBeUndefined();
    expect(definition.metadata?.web).toBeDefined();
  });

  test("exports topology without config, settings, or UI", () => {
    const bundle = buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      includeConfig: false,
      includeSettings: false,
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
      includeConfig: false,
      includeSettings: true,
      includeUI: false,
    });

    expect(bundle.contents).toEqual(["graph", "settings"]);
    expect(bundle.definition.nodes[0].config).toBeUndefined();
    expect(bundle.definition.edges?.[0].condition?.config).toBeUndefined();
    expect(bundle.settings?.environment).toEqual({ WORKDIR: "/workspace" });
    expect(bundle.ui).toBeUndefined();
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
      },
    };
    const bundle = buildGraphExportBundle({
      definition,
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      includeConfig: false,
      includeSettings: false,
      includeUI: true,
    });

    const virtualEdges = bundle.ui?.web.virtual_edges as Array<Record<string, unknown>>;
    expect(virtualEdges[0].condition).toEqual({ type: "state_equals" });
  });

  test("round trips an export bundle and restores UI metadata", () => {
    const source = buildGraphExportBundle({
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v3",
      runtimeSettings: runtimeSettings(),
      includeConfig: true,
      includeSettings: true,
      includeUI: true,
    });

    const imported = parseGraphImport(JSON.stringify(source));

    expect(imported.graphID).toBe("demo");
    expect(imported.graphVersion).toBe("v3");
    expect(imported.contents).toEqual(["graph", "config", "settings", "ui"]);
    expect(imported.definition.metadata?.web).toEqual({ positions: { task: { x: 20, y: 40 } } });
    expect(imported.settings?.environment).toEqual({ WORKDIR: "/workspace" });
    expect(imported.settings?.models[0].api_key_configured).toBe(false);
  });

  test("imports a plain Graph Definition JSON file", () => {
    const imported = parseGraphImport(JSON.stringify(graphDefinition()));

    expect(imported.graphID).toBe("demo");
    expect(imported.graphVersion).toBe("2.0");
    expect(imported.settings).toBeUndefined();
    expect(imported.contents).toEqual(["graph", "config", "ui"]);
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
    memory: { enabled: true, directory: ".local/memory" },
  };
}
