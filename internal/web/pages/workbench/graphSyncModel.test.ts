import { describe, expect, test } from "bun:test";
import type { GraphDefinition } from "../../types";
import {
  graphPublishRequired,
  graphUploadRequired,
  graphUploadSignature,
} from "./graphSyncModel";

describe("graph sync model", () => {
  test("ignores object key order when comparing graph uploads", () => {
    const first: GraphDefinition = {
      name: "example",
      nodes: [{ id: "input", type: "task", config: { second: 2, first: 1 } }],
      metadata: { owner: "team", web: { x: 10, y: 20 } },
    };
    const second: GraphDefinition = {
      metadata: { web: { y: 20, x: 10 }, owner: "team" },
      nodes: [{ config: { first: 1, second: 2 }, type: "task", id: "input" }],
      name: "example",
    };

    expect(graphUploadSignature(first, "graph", "v1")).toBe(graphUploadSignature(second, "graph", "v1"));
  });

  test("uploads only when graph content or identity changed", () => {
    const definition: GraphDefinition = { nodes: [{ id: "input", type: "task" }] };
    const signature = graphUploadSignature(definition, "graph", "v1");
    const synced = { signature, official: false };

    expect(graphUploadRequired(signature, synced)).toBe(false);
    expect(graphUploadRequired(graphUploadSignature(definition, "other", "v1"), synced)).toBe(true);
    expect(graphUploadRequired(graphUploadSignature(definition, "graph", "v2"), synced)).toBe(true);
    expect(graphUploadRequired(graphUploadSignature({ nodes: [] }, "graph", "v1"), synced)).toBe(true);
  });

  test("publishes a matching draft once and skips an already official graph", () => {
    const signature = graphUploadSignature({ nodes: [] }, "graph", "v1");
    expect(graphPublishRequired(signature, { signature, official: false })).toBe(true);
    expect(graphPublishRequired(signature, { signature, official: true })).toBe(false);
  });
});
