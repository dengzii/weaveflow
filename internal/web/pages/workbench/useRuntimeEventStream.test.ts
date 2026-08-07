import { describe, expect, test } from "bun:test";
import { parseRuntimeEventFrame } from "./useRuntimeEventStream";

describe("runtime event stream model", () => {
  test("parses runtime events and rejects malformed frames", () => {
    expect(parseRuntimeEventFrame('{"id":"event-1","graph_id":"graph-1","run_id":"run-1","type":"run.started","timestamp":"2026-07-31T00:00:00Z"}')).toEqual({
      id: "event-1",
      graph_id: "graph-1",
      run_id: "run-1",
      type: "run.started",
      timestamp: "2026-07-31T00:00:00Z",
    });
    expect(parseRuntimeEventFrame('{"id":"event-1"}')).toBeNull();
    expect(parseRuntimeEventFrame("[]")).toBeNull();
    expect(parseRuntimeEventFrame("not-json")).toBeNull();
  });
});
