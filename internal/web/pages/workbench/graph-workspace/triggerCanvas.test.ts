import { describe, expect, test } from "bun:test";
import type { GraphDefinition, Trigger } from "../../../types";
import {
  projectTriggerCanvasNodes,
  triggerConfigurationValid,
  triggerCanvasNodeID,
  withCleanTriggerCanvasPositions,
  withTriggerCanvasPosition,
} from "./triggerCanvas";

const definition: GraphDefinition = {
  version: "1.0",
  name: "graph-a",
  entry_point: "entry",
  finish_point: "entry",
  nodes: [{ id: "entry", type: "message" }],
  edges: [],
  metadata: {
    web: {
      positions: { entry: { x: 0, y: 40 } },
      trigger_nodes: {
        incoming: { x: -500, y: 20 },
        stale: { x: -900, y: 20 },
      },
    },
  },
};

const triggers: Trigger[] = [
  trigger("other", "graph-b", "schedule"),
  trigger("nightly", "graph-a", "schedule"),
  trigger("incoming", "graph-a", "webhook"),
];

describe("trigger canvas projection", () => {
  test("filters by graph, uses namespaced IDs, and keeps stored positions", () => {
    const nodes = projectTriggerCanvasNodes(definition, "graph-a", triggers, ["__start__"], []);
    expect(nodes.map((node) => node.trigger.id)).toEqual(["incoming", "nightly"]);
    expect(nodes[0]?.position).toEqual({ x: -500, y: 20 });
    expect(nodes[1]?.position.x).toBe(-520);
    expect(nodes.every((node) => node.valid)).toBe(true);
    expect(nodes.every((node) => !definition.nodes.some((real) => real.id === node.canvas_id))).toBe(true);
  });

  test("uses the registered chat channel title as the Chat Trigger card label", () => {
    const chat = trigger("assistant", "graph-a", "chat");
    chat.name = "Chat";
    chat.chat = { channel: "weixin", stream_updates: true };

    const nodes = projectTriggerCanvasNodes(definition, "graph-a", [chat], ["__start__"], [{
      id: "weixin",
      title: "WeChat Bot",
      config_schema: {},
    }]);

    expect(nodes[0]?.label).toBe("WeChat Bot");
  });

  test("avoids a real-node collision in the canvas namespace", () => {
    const base = triggerCanvasNodeID("incoming");
    expect(triggerCanvasNodeID("incoming", new Set([base]))).toBe(`${base}:2`);
  });

  test("moving a trigger changes only metadata.web.trigger_nodes", () => {
    const moved = withTriggerCanvasPosition(definition, "incoming", { x: -320, y: 80 }, ["incoming", "nightly"]);
    expect(moved.nodes).toEqual(definition.nodes);
    expect(moved.edges).toEqual(definition.edges);
    expect(moved.metadata).toEqual({
      web: {
        positions: { entry: { x: 0, y: 40 } },
        trigger_nodes: { incoming: { x: -320, y: 80 } },
      },
    });
  });

  test("cleanup removes stale trigger IDs without touching other web metadata", () => {
    const cleaned = withCleanTriggerCanvasPositions(definition, ["incoming"]);
    expect(cleaned.metadata).toEqual({
      web: {
        positions: { entry: { x: 0, y: 40 } },
        trigger_nodes: { incoming: { x: -500, y: 20 } },
      },
    });
  });

  test("marks incomplete trigger configuration as invalid", () => {
    const invalid = trigger("broken", "graph-a", "schedule");
    invalid.schedule = {} as Trigger["schedule"];

    expect(() => triggerConfigurationValid(invalid)).not.toThrow();
    expect(triggerConfigurationValid(invalid)).toBe(false);
  });

  test("validates channel-neutral chat trigger settings", () => {
    const chat = trigger("chat", "graph-a", "chat");
    chat.chat = {
      stream_updates: true,
      stream_node_ids: ["answer"],
      history_limit: 10,
      state_bindings: {
        conversation: "scopes.agent.conversation",
        raw_history: "scopes.chat.raw_history",
        user_id: "scopes.chat.user_id",
      },
    };
    expect(triggerConfigurationValid(chat)).toBe(true);

    chat.chat.stream_node_ids = [""];
    expect(triggerConfigurationValid(chat)).toBe(false);
  });

  test("rejects invalid chat history and state binding settings", () => {
    const chat = trigger("chat", "graph-a", "chat");
    chat.chat = { history_limit: 501 };
    expect(triggerConfigurationValid(chat)).toBe(false);

    chat.chat = { state_bindings: { raw_history: "runtime.chat.history" } };
    expect(triggerConfigurationValid(chat)).toBe(false);

    chat.chat = { state_bindings: { user_id: "shared.request.input.user_id" } };
    expect(triggerConfigurationValid(chat)).toBe(false);

    chat.chat = { state_bindings: { raw_history: "scopes.chat", message_id: "scopes.chat.message_id" } };
    expect(triggerConfigurationValid(chat)).toBe(false);

    chat.chat = { state_bindings: { conversation: "scopes.chat", message_id: "scopes.chat.messages.message_id" } };
    expect(triggerConfigurationValid(chat)).toBe(false);
  });
});

function trigger(id: string, graphID: string, type: Trigger["type"]): Trigger {
  return {
    id,
    type,
    enabled: true,
    target: { graph_id: graphID },
    webhook: type === "webhook" ? {} : undefined,
    schedule: type === "schedule" ? { cron: "0 * * * *" } : undefined,
    chat: type === "chat" ? { stream_updates: true } : undefined,
    created_at: "2026-07-29T00:00:00Z",
    updated_at: "2026-07-29T00:00:00Z",
  };
}
