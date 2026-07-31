import { describe, expect, test } from "bun:test";
import type {
  DynamicStatePortDefinition,
  GraphDefinition,
  RegistryInfo,
  StateBinding,
  StatePortDefinition,
} from "../../../types";
import {
  bindingPathMetadata,
  compatibleBindingPaths,
  dynamicStateAliasError,
  sanitizeHTMLID,
} from "./stateBindingsModel";

const inputPort: StatePortDefinition = {
  name: "input",
  schema: { type: "string" },
  mode: "read",
};

describe("state bindings model", () => {
  test("suggests defaults, selected module fields, and compatible producer paths", () => {
    expect(compatibleBindingPaths(inputPort, "consumer", graphDefinition(), registry())).toEqual([
      "shared.input",
      "scopes.consumer.input",
      "shared.request.input",
      "shared.generated",
    ]);
  });

  test("describes a path using its schema, module, producers, and consumers", () => {
    expect(bindingPathMetadata("shared.generated", inputPort, graphDefinition(), registry())).toBe(
      "type string · produced by producer · consumed by consumer"
    );
    expect(bindingPathMetadata("shared.request.input", inputPort, graphDefinition(), registry())).toBe(
      "type string · module conversation@v1"
    );
  });

  test("validates dynamic aliases against static and existing bindings", () => {
    const dynamicPorts: DynamicStatePortDefinition = {
      name_pattern: "^[a-z][a-z0-9_]*$",
      schema: { type: "string" },
      mode: "read",
      merge_strategy: "replace",
    };
    const bindings: Record<string, StateBinding> = {
      current: { path: "shared.current" },
      existing: { path: "shared.existing" },
    };
    const staticNames = new Set(["input"]);

    expect(dynamicStateAliasError("", "current", bindings, staticNames, dynamicPorts)).toBe("Alias is required.");
    expect(dynamicStateAliasError("input", "current", bindings, staticNames, dynamicPorts)).toContain("static port");
    expect(dynamicStateAliasError("existing", "current", bindings, staticNames, dynamicPorts)).toContain("already bound");
    expect(dynamicStateAliasError("Not Valid", "current", bindings, staticNames, dynamicPorts)).toContain("must match");
    expect(dynamicStateAliasError("renamed", "current", bindings, staticNames, dynamicPorts)).toBe("");
  });

  test("builds stable HTML ids", () => {
    expect(sanitizeHTMLID("node one/input.value")).toBe("node-one-input-value");
  });
});

function graphDefinition(): GraphDefinition {
  return {
    version: "2.0",
    state_modules: [{ name: "conversation", version: "v1" }],
    nodes: [
      {
        id: "producer",
        type: "producer",
        state: { output: { path: "shared.generated" } },
      },
      {
        id: "consumer",
        type: "consumer",
        state: { input: { path: "shared.generated" } },
      },
      {
        id: "other-reader",
        type: "consumer",
        state: { input: { path: "shared.read_only" } },
      },
    ],
  };
}

function registry(): RegistryInfo {
  return {
    state_modules: [
      {
        name: "conversation",
        version: "v1",
        fields: [
          { path: "shared.request.input", schema: { type: "string" } },
          { path: "shared.request.count", schema: { type: "number" } },
        ],
      },
    ],
    capabilities: [],
    node_groups: [],
    node_types: [
      {
        type: "producer",
        state_ports: [{ name: "output", schema: { type: "string" }, mode: "write" }],
      },
      {
        type: "consumer",
        state_ports: [inputPort],
      },
    ],
    conditions: [],
    graph_schema: {},
  };
}
