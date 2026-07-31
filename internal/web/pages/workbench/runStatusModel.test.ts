import { describe, expect, test } from "bun:test";
import type { RuntimeEvent } from "../../types";
import { eventMatchesFilters, uniqueSorted } from "./runStatusModel";

describe("run status model", () => {
  test("matches all selected event facets and payload keywords", () => {
    const event = runtimeEvent();

    expect(
      eventMatchesFilters(event, {
        mode: "include",
        types: ["nodes.failed"],
        nodes: ["worker"],
        keyword: "timeout",
      })
    ).toBe(true);
    expect(
      eventMatchesFilters(event, {
        mode: "include",
        types: ["nodes.failed"],
        nodes: ["other"],
        keyword: "timeout",
      })
    ).toBe(false);
  });

  test("inverts active criteria in exclude mode without hiding unfiltered events", () => {
    const event = runtimeEvent();

    expect(
      eventMatchesFilters(event, {
        mode: "exclude",
        types: ["nodes.failed"],
        nodes: [],
        keyword: "",
      })
    ).toBe(false);
    expect(
      eventMatchesFilters(event, {
        mode: "exclude",
        types: [],
        nodes: [],
        keyword: "",
      })
    ).toBe(true);
  });

  test("normalizes facet options into a stable unique list", () => {
    expect(uniqueSorted([" warning ", "nodes.failed", "warning", ""])).toEqual([
      "nodes.failed",
      "warning",
    ]);
  });
});

function runtimeEvent(): RuntimeEvent {
  return {
    id: "event-1",
    run_id: "run-1",
    step_id: "step-1",
    node_id: "worker",
    type: "nodes.failed",
    timestamp: "2026-07-30T02:00:01Z",
    payload: { error: "request timeout" },
  };
}
