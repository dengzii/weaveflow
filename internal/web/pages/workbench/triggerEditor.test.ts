import { describe, expect, test } from "bun:test";
import type { GraphDefinition, Trigger } from "../../types";
import {
  buildTriggerInitialState,
  buildTriggerPayload,
  triggerEditorValues,
  triggerInitialStateEntries,
  triggerStatePathSuggestions,
  webhookTriggerURLs,
} from "./triggerEditor";

const webhook: Trigger = {
  id: "incoming",
  type: "webhook",
  enabled: true,
  concurrency: "skip",
  target: { graph_id: "graph-a" },
  initial_state: { shared: { tenant: "tenant-a" } },
  webhook: { state_mappings: [{ parameter: "user.id", state_path: "shared.user.id" }] },
  created_at: "2026-07-29T00:00:00Z",
  updated_at: "2026-07-29T00:00:00Z",
};

describe("trigger editor payload", () => {
  test("preserves the full update contract while omitting an unchanged api_key", () => {
    const values = triggerEditorValues(webhook, { graph_id: "fallback" });
    values.enabled = false;
    const payload = buildTriggerPayload(values, webhook);

    expect(payload).toEqual({
      name: undefined,
      type: "webhook",
      enabled: false,
      concurrency: "skip",
      target: { graph_id: "graph-a" },
      initial_state: { shared: { tenant: "tenant-a" } },
      webhook: {
        api_key: undefined,
        state_mappings: [{ parameter: "user.id", state_path: "shared.user.id" }],
      },
    });
  });

  test("rejects incomplete webhook mappings", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" });
    values.mappings = [{ parameter: "user.id", state_path: "" }];
    expect(() => buildTriggerPayload(values, null)).toThrow("both a parameter and state path");
  });

  test("uses the requested type for a new trigger", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "schedule");

    expect(values.type).toBe("schedule");
    expect(values.cron).toBe("*/5 * * * *");
    expect(values.initialStateEntries).toEqual([]);
  });

  test("edits initial state as path and value entries", () => {
    expect(triggerInitialStateEntries({
      shared: { tenant: { id: "tenant-a" } },
      scopes: { agent: { mode: "review" } },
    })).toEqual([
      { path: "shared.tenant.id", value: "tenant-a" },
      { path: "scopes.agent.mode", value: "review" },
    ]);

    expect(buildTriggerInitialState([
      { path: "shared.tenant.id", value: "tenant-a" },
      { path: "shared.tenant.active", value: "true" },
      { path: "shared.tenant.retries", value: "3" },
      { path: "scopes.agent.mode", value: "review" },
      { path: "scopes.agent.note", value: "" },
      { path: "", value: "" },
    ])).toEqual({
      shared: { tenant: { id: "tenant-a", active: true, retries: 3 } },
      scopes: { agent: { mode: "review", note: "" } },
    });
  });

  test("rejects invalid initial state paths", () => {
    expect(() => buildTriggerInitialState([{ path: "runtime.run_id", value: "spoofed" }])).toThrow("not allowed");
    expect(() => buildTriggerInitialState([{ path: "shared.trigger.id", value: "spoofed" }])).toThrow("reserved");
    expect(() => buildTriggerInitialState([
      { path: "shared.tenant", value: "a" },
      { path: "shared.tenant", value: "b" },
    ])).toThrow("duplicate");
    expect(() => buildTriggerInitialState([
      { path: "shared.tenant", value: "a" },
      { path: "shared.tenant.id", value: "b" },
    ])).toThrow("overlapping");
  });

  test("builds full webhook URLs with the api_key query parameter", () => {
    expect(webhookTriggerURLs("incoming hook")).toEqual({
      post: "http://localhost:8080/triggers/incoming%20hook?api_key=YOUR_API_KEY",
      get: "http://localhost:8080/triggers/incoming%20hook/webhook?api_key=YOUR_API_KEY",
    });
  });

  test("suggests existing node and condition state bindings", () => {
    const definition: GraphDefinition = {
      nodes: [
        {
          id: "input",
          state: {
            value: { path: " shared.request.input " },
            ignored: { path: "" },
          },
        },
        {
          id: "agent",
          state: {
            input: { path: "shared.request.input" },
            result: { path: "shared.final.answer" },
          },
        },
      ],
      edges: [{
        from: "input",
        to: "agent",
        condition: {
          type: "has_input",
          state: {
            input: { path: "scopes.input.value" },
            blank: { path: "   " },
          },
        },
      }],
    };

    expect(triggerStatePathSuggestions(definition)).toEqual([
      "shared.request.input",
      "shared.final.answer",
      "scopes.input.value",
    ]);
    expect(triggerStatePathSuggestions(null)).toEqual([]);
  });
});
