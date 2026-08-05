import { describe, expect, test } from "bun:test";
import type { GraphDefinition, RuntimeSettingsUpdate } from "../../types";
import {
  graphAnalysisSignature,
  graphSaveIdentity,
  graphSaveSignature,
  isGraphSavePending,
} from "./graphSyncModel";

describe("graph sync model", () => {
  test("excludes non-executable metadata from graph analysis signatures", () => {
    const first: GraphDefinition = {
      nodes: [{ id: "input", type: "task", config: { prompt: "hello" } }],
      metadata: { web: { positions: { input: { x: 10, y: 20 } } } },
    };
    const moved: GraphDefinition = {
      ...first,
      metadata: { web: { positions: { input: { x: 300, y: 400 } } } },
    };

    expect(graphAnalysisSignature(first)).toBe(graphAnalysisSignature(moved));
  });

  test("changes graph analysis signatures for executable graph changes", () => {
    const first: GraphDefinition = {
      nodes: [{ id: "input", type: "task", config: { prompt: "hello" } }],
    };
    const changed: GraphDefinition = {
      nodes: [{ id: "input", type: "task", config: { prompt: "updated" } }],
    };

    expect(graphAnalysisSignature(first)).not.toBe(graphAnalysisSignature(changed));
  });

  test("tracks the complete server save payload", () => {
    const definition: GraphDefinition = {
      nodes: [{ id: "input", type: "task", config: { prompt: "hello" } }],
    };
    const settings: RuntimeSettingsUpdate = {
      environment: { MODE: "test" },
      models: [],
      memory: { enabled: false },
    };
    const saved = graphSaveSignature(definition, settings, " graph ", " v1 ");

    expect(saved).toBe(graphSaveSignature(definition, settings, "graph", "v1"));
    expect(saved).not.toBe(graphSaveSignature(
      { ...definition, nodes: [{ id: "input", type: "task", config: { prompt: "changed" } }] },
      settings,
      "graph",
      "v1"
    ));
    expect(saved).not.toBe(graphSaveSignature(
      definition,
      { ...settings, environment: { MODE: "production" } },
      "graph",
      "v1"
    ));
    expect(graphSaveIdentity(" graph ", " v1 ")).toBe(graphSaveIdentity("graph", "v1"));
    expect(isGraphSavePending(saved, saved)).toBe(false);
    expect(isGraphSavePending(saved, undefined)).toBe(true);
    expect(isGraphSavePending(saved, `${saved}-changed`)).toBe(true);
  });
});
