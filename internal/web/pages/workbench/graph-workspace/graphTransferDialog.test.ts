import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { GraphDefinition, RuntimeSettings, Trigger } from "../../../types";
import { GraphTitleMenu } from "./GraphTitleMenu";
import { GraphTransferDialog } from "./GraphTransferDialog";

describe("graph transfer UI", () => {
  test("shows graph actions at the end of local and server graph items", () => {
    const markup = renderToStaticMarkup(createElement(GraphTitleMenu, {
      activeCacheID: "local-demo",
      definition: graphDefinition(),
      graphs: [{
        id: "local-demo",
        title: "demo",
        graphId: "demo",
        graphVersion: "v1",
        definition: graphDefinition(),
        runtimeSettings: runtimeSettings(),
        nodeCount: 1,
        serverGraph: false,
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
      }, {
        id: "server:remote:session",
        title: "remote",
        graphId: "remote",
        graphVersion: "v1",
        nodeCount: 2,
        serverGraph: true,
        latestSession: "session",
        createdAt: "2026-01-02T00:00:00Z",
        updatedAt: "2026-01-02T00:00:00Z",
      }],
      graphID: "demo",
      open: true,
      graphSwitchDisabled: false,
      onCreateGraph: () => undefined,
      onDeleteGraph: () => undefined,
      onExportGraph: () => undefined,
      onImportGraph: () => undefined,
      onLoadGraph: () => undefined,
      onOpenChange: () => undefined,
    }));

    expect(markup).toContain('aria-label="Export demo"');
    expect(markup).toContain('aria-label="Delete demo"');
    expect(markup).toContain('aria-label="Delete remote"');
    expect(markup).toContain("New graph");
    expect(markup).toContain("Import graph");
    expect(markup).not.toContain("*demo");
  });

  test("renders export contents as a checkbox list", () => {
    const markup = renderToStaticMarkup(createElement(GraphTransferDialog, {
      mode: "export",
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      triggers: graphTriggers(),
      existingGraphIDs: ["demo"],
      onClose: () => undefined,
      onImport: () => true,
    }));

    expect(markup).toContain(">Graph<");
    expect(markup).toContain(">Config<");
    expect(markup).toContain(">Settings<");
    expect(markup).toContain(">Triggers<");
    expect(markup).toContain("UI information");
    expect(markup.match(/type="checkbox"/g)).toHaveLength(5);
    expect(markup).not.toContain("API keys and secrets");
    expect(markup).not.toContain("secret-like environment values");
    expect(markup).toContain("z-[200]");
  });
});

function graphDefinition(): GraphDefinition {
  return {
    version: "1.0",
    name: "demo",
    nodes: [{ id: "task", type: "task" }],
  };
}

function graphTriggers(): Trigger[] {
  return [{
    id: "hook",
    type: "webhook",
    enabled: true,
    target: { graph_id: "demo" },
    webhook: {},
    created_at: "",
    updated_at: "",
  }];
}

function runtimeSettings(): RuntimeSettings {
  return {
    environment: {},
    models: [],
  };
}
