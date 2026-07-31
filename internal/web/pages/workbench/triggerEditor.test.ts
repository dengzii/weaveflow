import { describe, expect, test } from "bun:test";
import type { GraphDefinition, Trigger } from "../../types";
import {
  buildTriggerInitialState,
  buildTriggerPayload,
  chatChannelDefaultConfig,
  editableChatChannelSchema,
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

  test("builds a registered HTTP chat channel trigger", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    values.streamNodeIDs = "answer, reviewer answer";

    expect(buildTriggerPayload(values, null)).toEqual({
      name: undefined,
      type: "chat",
      enabled: true,
      concurrency: "parallel",
      target: { graph_id: "graph-a" },
      chat: {
        channel: "http",
        channel_config: {},
        reply_path: "shared.final.answer",
        stream_updates: true,
        stream_node_ids: ["answer", "reviewer"],
      },
    });
  });

  test("adds a confirmed chat setup session only to the transport payload", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    values.chatChannel = "weixin";

    expect(buildTriggerPayload(values, null, " setup-session ")).toMatchObject({
      chat: { channel: "weixin" },
      chat_setup_session_id: "setup-session",
    });
    expect(buildTriggerPayload(values, null)).not.toHaveProperty("chat_setup_session_id");
  });

  test("keeps write-only channel fields editable without requiring the stored secret again", () => {
    const definition = {
      id: "wecom",
      title: "WeCom",
      config_schema: {
        type: "object",
        properties: {
          bot_id: { type: "string" },
          secret: { type: "string", writeOnly: true },
        },
        required: ["bot_id", "secret"],
      },
    };
    expect(editableChatChannelSchema(definition, false)?.required).toEqual(["bot_id", "secret"]);
    expect(editableChatChannelSchema(definition, true)?.required).toEqual(["bot_id"]);
    expect(chatChannelDefaultConfig({
      ...definition,
      config_schema: {
        ...definition.config_schema,
        properties: {
          ...definition.config_schema.properties,
          endpoint: { type: "string", default: "wss://example.test" },
        },
      },
    })).toEqual({ endpoint: "wss://example.test" });
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
