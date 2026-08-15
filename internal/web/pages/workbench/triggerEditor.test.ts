import { describe, expect, test } from "bun:test";
import type { GraphDefinition, Trigger } from "../../types";
import {
  buildTriggerInitialState,
  buildTriggerPayload,
  chatChannelDefaultConfig,
  editableChatChannelSchema,
  triggerDraftFromEditorValues,
  triggerEditorValues,
  triggerInitialStateEntries,
  triggerStatePathSuggestions,
  triggerTypeName,
  webhookCurlCommand,
  webhookTriggerURL,
} from "./triggerEditor";

const webhook: Trigger = {
  id: "incoming",
  type: "webhook",
  enabled: true,
  concurrency: "skip",
  target: { graph_id: "graph-a" },
  credential: { source: "env", ref: "TRIGGER_TOKEN" },
  initial_state: { shared: { tenant: "tenant-a" } },
  webhook: {
    state_bindings: {
      input: "shared.request.input",
      metadata: "shared.request.metadata",
      trigger_id: "shared.trigger.id",
      trigger_type: "shared.trigger.type",
    },
    state_mappings: [{ parameter: "user.id", state_path: "shared.user.id" }],
  },
  created_at: "2026-07-29T00:00:00Z",
  updated_at: "2026-07-29T00:00:00Z",
};

describe("trigger editor payload", () => {
  test("preserves the full update contract with a credential reference", () => {
    const values = triggerEditorValues(webhook, { graph_id: "fallback" });
    values.enabled = false;
    const payload = buildTriggerPayload(values, webhook);

    expect(payload).toEqual({
      id: "incoming",
      name: "Webhook",
      type: "webhook",
      enabled: false,
      concurrency: "skip",
      credential: { source: "env", ref: "TRIGGER_TOKEN" },
      initial_state: { shared: { tenant: "tenant-a" } },
      webhook: {
        state_bindings: {
          input: "shared.request.input",
          metadata: "shared.request.metadata",
          trigger_id: "shared.trigger.id",
          trigger_type: "shared.trigger.type",
        },
        state_mappings: [{ parameter: "user.id", state_path: "shared.user.id" }],
      },
    });
  });

  test("rejects incomplete webhook mappings", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" });
    values.id = "incoming";
    values.mappings = [{ parameter: "user.id", state_path: "" }];
    expect(() => buildTriggerPayload(values, null)).toThrow("both a parameter and state path");
  });

  test("uses the requested type for a new trigger", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "schedule");

    expect(values.type).toBe("schedule");
    expect(values.name).toBe("Schedule");
    expect(values.cron).toBe("*/5 * * * *");
    expect(values.initialStateEntries).toEqual([]);
    expect(values.stateBindings).toEqual({
      input: "shared.request.input",
      metadata: "shared.request.metadata",
      trigger_id: "shared.trigger.id",
      trigger_type: "shared.trigger.type",
    });
  });

  test("keeps incomplete trigger edits in the local draft until server save validation", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "schedule");
    values.id = "schedule-draft";
    values.cron = "";

    expect(triggerDraftFromEditorValues(values, null)).toMatchObject({
      id: "schedule-draft",
      schedule: { cron: "" },
    });
    expect(() => buildTriggerPayload(values, null)).toThrow("cron is required");
  });

  test("creates an imported schedule with its ID and input intact", () => {
    const imported: Trigger = {
      id: "scheduled-import",
      type: "schedule",
      enabled: true,
      target: { graph_id: "graph-import" },
      schedule: {
        cron: "0 * * * *",
        timezone: "UTC",
        input: { tenant: "demo" },
      },
      created_at: "",
      updated_at: "",
    };
    const values = triggerEditorValues(imported, imported.target!);

    expect(buildTriggerPayload(values, imported)).toMatchObject({
      id: "scheduled-import",
      schedule: { input: { tenant: "demo" } },
    });
  });

  test("uses the edited trigger name in replacement payloads", () => {
    const existing = { ...webhook, name: "Webhook 3" };
    const values = triggerEditorValues(existing, { graph_id: "fallback" });
    values.name = "Renamed";

    expect(buildTriggerPayload(values, existing)).toMatchObject({ name: "Renamed" });
    expect(triggerTypeName("webhook")).toBe("Webhook");
    expect(triggerTypeName("schedule")).toBe("Schedule");
    expect(triggerTypeName("chat")).toBe("Chat");
  });

  test("builds a registered HTTP chat channel trigger", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    values.id = "chat";
    values.streamNodeIDs = "answer, reviewer answer";

    expect(buildTriggerPayload(values, null)).toEqual({
      id: "chat",
      name: "Chat",
      type: "chat",
      enabled: true,
      concurrency: "parallel",
      chat: {
        channel: "http",
        channel_config: {},
        stream_updates: true,
        stream_node_ids: ["answer", "reviewer"],
        state_bindings: { input: "shared.request.input" },
      },
    });
  });

  test("clears the chat input binding without a runtime fallback", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    values.id = "chat";
    values.stateBindings.input = "";

    const payload = buildTriggerPayload(values, null);
    expect(payload.chat).not.toHaveProperty("state_bindings");
    expect(triggerDraftFromEditorValues(values, null).chat).not.toHaveProperty("state_bindings");
  });

  test("builds optional chat history and metadata state bindings", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    values.id = "chat";
    values.chatHistoryLimit = "10";
    values.stateBindings.conversation = " scopes.agent.conversation ";
    values.stateBindings.raw_history = " scopes.chat.raw_history ";
    values.stateBindings.trigger_id = "scopes.chat.trigger_id";
    values.stateBindings.channel = "scopes.chat.channel";
    values.stateBindings.user_id = "scopes.chat.user_id";
    values.stateBindings.conversation_id = "scopes.chat.conversation_id";
    values.stateBindings.message_id = "scopes.chat.message_id";

    expect(buildTriggerPayload(values, null)).toMatchObject({
      chat: {
        history_limit: 10,
        state_bindings: {
          input: "shared.request.input",
          conversation: "scopes.agent.conversation",
          raw_history: "scopes.chat.raw_history",
          trigger_id: "scopes.chat.trigger_id",
          channel: "scopes.chat.channel",
          user_id: "scopes.chat.user_id",
          conversation_id: "scopes.chat.conversation_id",
          message_id: "scopes.chat.message_id",
        },
      },
    });
  });

  test("loads optional chat state settings when editing", () => {
    const chat: Trigger = {
      id: "chat",
      type: "chat",
      enabled: true,
      target: { graph_id: "graph-a" },
      chat: {
        history_limit: 25,
        state_bindings: {
          conversation: "scopes.agent.conversation",
          raw_history: "scopes.chat.raw_history",
          user_id: "scopes.chat.user_id",
        },
      },
      created_at: "2026-07-29T00:00:00Z",
      updated_at: "2026-07-29T00:00:00Z",
    };

    const values = triggerEditorValues(chat, { graph_id: "fallback" });
    expect(values.chatHistoryLimit).toBe("25");
    expect(values.stateBindings.conversation).toBe("scopes.agent.conversation");
    expect(values.stateBindings.raw_history).toBe("scopes.chat.raw_history");
    expect(values.stateBindings.user_id).toBe("scopes.chat.user_id");
    expect(values.stateBindings.message_id).toBeUndefined();
  });

  test("rejects invalid chat history rounds and state bindings", () => {
    for (const historyLimit of ["-1", "1.5", "501"]) {
      const values = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
      values.id = "chat";
      values.chatHistoryLimit = historyLimit;
      expect(() => buildTriggerPayload(values, null)).toThrow("integer between 0 and 500");
    }

    const invalidSection = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    invalidSection.id = "chat";
    invalidSection.stateBindings.raw_history = "runtime.chat.history";
    expect(() => buildTriggerPayload(invalidSection, null)).toThrow("section runtime is not allowed");

    const inputOverlap = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    inputOverlap.id = "chat";
    inputOverlap.stateBindings.user_id = "shared.request.input.user_id";
    expect(() => buildTriggerPayload(inputOverlap, null)).toThrow("overlaps input state path");

    const bindingOverlap = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    bindingOverlap.id = "chat";
    bindingOverlap.stateBindings.raw_history = "scopes.chat";
    bindingOverlap.stateBindings.user_id = "scopes.chat.user_id";
    expect(() => buildTriggerPayload(bindingOverlap, null)).toThrow("overlaps raw history state path");

    const conversationOverlap = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    conversationOverlap.id = "chat";
    conversationOverlap.stateBindings.conversation = "scopes.chat";
    conversationOverlap.stateBindings.user_id = "scopes.chat.messages.user_id";
    expect(() => buildTriggerPayload(conversationOverlap, null)).toThrow("overlaps conversation state path");
  });

  test("rejects overlaps across bindings, webhook mappings, and initial state", () => {
    const webhookValues = triggerEditorValues(null, { graph_id: "graph-a" }, "webhook");
    webhookValues.id = "hook";
    webhookValues.mappings = [{ parameter: "user", state_path: "shared.request.input.user" }];
    expect(() => buildTriggerPayload(webhookValues, null)).toThrow("overlaps input state path");

    const scheduleValues = triggerEditorValues(null, { graph_id: "graph-a" }, "schedule");
    scheduleValues.id = "schedule";
    scheduleValues.initialStateEntries = [{ path: "shared.request.input", value: "configured" }];
    expect(() => buildTriggerPayload(scheduleValues, null)).toThrow("overlaps input state path");
  });

  test("adds a confirmed chat setup session only to the transport payload", () => {
    const values = triggerEditorValues(null, { graph_id: "graph-a" }, "chat");
    values.id = "chat";
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
          endpoint: { type: "string" },
          secret: { type: "string", writeOnly: true },
        },
        required: ["bot_id", "secret"],
      },
    };
    const createSchema = editableChatChannelSchema(definition, false);
    expect(createSchema?.required).toEqual(["bot_id", "secret"]);
    expect(Object.keys(createSchema?.properties as Record<string, unknown>)).toEqual([
      "bot_id",
      "secret",
      "endpoint",
    ]);
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
    expect(buildTriggerInitialState([{ path: "shared.trigger.id", value: "explicit" }])).toEqual({
      shared: { trigger: { id: "explicit" } },
    });
    expect(() => buildTriggerInitialState([
      { path: "shared.tenant", value: "a" },
      { path: "shared.tenant", value: "b" },
    ])).toThrow("duplicate");
    expect(() => buildTriggerInitialState([
      { path: "shared.tenant", value: "a" },
      { path: "shared.tenant.id", value: "b" },
    ])).toThrow("overlapping");
  });

  test("builds the Graph-scoped webhook URL", () => {
    expect(webhookTriggerURL("graph a", "incoming hook")).toBe(
      "http://localhost:8080/graphs/graph%20a/triggers/incoming%20hook/webhook"
    );
  });

  test("builds a curl webhook test command", () => {
    expect(webhookCurlCommand("http://localhost:8080/graphs/graph-a/triggers/incoming/webhook")).toBe(
      'curl -X POST "http://localhost:8080/graphs/graph-a/triggers/incoming/webhook" -H "Authorization: Bearer $TRIGGER_TOKEN" -H "Content-Type: application/json" -d "{}"'
    );
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
