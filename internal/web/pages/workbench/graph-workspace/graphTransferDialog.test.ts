import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { GraphDefinition, RuntimeSettings } from "../../../types";
import { GraphTitleMenu } from "./GraphTitleMenu";
import { GraphTransferDialog } from "./GraphTransferDialog";

describe("graph transfer UI", () => {
  test("offers import and export from the graph title menu", () => {
    const markup = renderToStaticMarkup(createElement(GraphTitleMenu, {
      activeCacheID: "",
      definition: graphDefinition(),
      graphs: [],
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

    expect(markup).toContain("Import graph");
    expect(markup).toContain("Export graph");
    expect(markup).not.toContain("*demo");
  });

  test("renders export contents as a checkbox list", () => {
    const markup = renderToStaticMarkup(createElement(GraphTransferDialog, {
      mode: "export",
      definition: graphDefinition(),
      graphID: "demo",
      graphVersion: "v1",
      runtimeSettings: runtimeSettings(),
      onClose: () => undefined,
      onImport: () => true,
    }));

    expect(markup).toContain(">Graph<");
    expect(markup).toContain(">Config<");
    expect(markup).toContain(">Settings<");
    expect(markup).toContain("UI information");
    expect(markup.match(/type="checkbox"/g)).toHaveLength(4);
    expect(markup).toContain("API keys and secret-like environment values are excluded.");
  });
});

function graphDefinition(): GraphDefinition {
  return {
    version: "2.0",
    name: "demo",
    nodes: [{ id: "task", type: "task" }],
  };
}

function runtimeSettings(): RuntimeSettings {
  return {
    environment: {},
    models: [],
    memory: { enabled: false },
  };
}
