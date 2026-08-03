import { describe, expect, test } from "bun:test";
import type { Trigger, TriggerType } from "../../../types";
import { nextTriggerName, triggersForGraph, uniqueTriggerIDs, upsertTrigger } from "./graphTriggerModel";

describe("graph trigger model", () => {
  test("filters triggers by the normalized graph ID", () => {
    const triggers = [
      trigger("first", " graph-a "),
      trigger("second", "graph-b"),
      trigger("third", "graph-a"),
    ];

    expect(triggersForGraph(triggers, " graph-a ").map((item) => item.id)).toEqual(["first", "third"]);
    expect(triggersForGraph(triggers, "   ")).toEqual([]);
  });

  test("replaces an existing trigger in place and appends a new trigger", () => {
    const first = trigger("first", "graph-a");
    const second = trigger("second", "graph-a");
    const updated = { ...first, name: "Updated" };

    expect(upsertTrigger([first, second], updated)).toEqual([updated, second]);
    expect(upsertTrigger([first], second)).toEqual([first, second]);
  });

  test("builds stable unique trigger ID lists", () => {
    expect(uniqueTriggerIDs(
      [trigger("first", "graph-a"), trigger("second", "graph-a")],
      [" second ", "third", ""]
    )).toEqual(["first", "second", "third"]);
  });

  test("increments generated names for triggers of the same type", () => {
    const unnamed = trigger("first", "graph-a");
    const second = { ...trigger("second", "graph-a"), name: "Webhook 2" };
    const fourth = { ...trigger("fourth", "graph-a"), name: "Webhook 4" };
    const custom = { ...trigger("custom", "graph-a"), name: "Incoming" };
    const schedule = { ...trigger("schedule", "graph-a", "schedule"), name: "Schedule" };

    expect(nextTriggerName([], "webhook")).toBe("Webhook");
    expect(nextTriggerName([unnamed, second, fourth, custom, schedule], "webhook")).toBe("Webhook 5");
    expect(nextTriggerName([unnamed, second, fourth, custom, schedule], "schedule")).toBe("Schedule 2");
    expect(nextTriggerName([unnamed, second, fourth, custom, schedule], "chat")).toBe("Chat");
  });
});

function trigger(id: string, graphID: string, type: TriggerType = "webhook"): Trigger {
  return {
    id,
    type,
    enabled: true,
    concurrency: "parallel",
    target: { graph_id: graphID },
    created_at: "2026-07-31T00:00:00Z",
    updated_at: "2026-07-31T00:00:00Z",
  };
}
