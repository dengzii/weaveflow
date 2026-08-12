import { afterEach, describe, expect, test } from "bun:test";
import {
  ApiError,
  analyzeInitialStateRequirements,
  createGraphSession,
  getRunInspection,
  listGraphs,
  listRuns,
  replaceTriggers,
  startRun,
} from "./api";

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe("server API client", () => {
  test("consumes Graph and Run pagination without legacy list requests", async () => {
    const requests: string[] = [];
    globalThis.fetch = (async (input) => {
      const url = String(input);
      requests.push(url);
      if (url.includes("/graphs/graph-a/runs")) {
        return jsonResponse(requests.filter((item) => item.includes("/runs")).length === 1
          ? { items: [run("run-2")], next_cursor: "1" }
          : { items: [run("run-1")], next_cursor: "" });
      }
      return jsonResponse(requests.filter((item) => !item.includes("/runs")).length === 1
        ? { items: [graph("graph-a")], next_cursor: "1" }
        : { items: [graph("graph-b")], next_cursor: "" });
    }) as typeof fetch;

    expect((await listGraphs()).map((item) => item.id)).toEqual(["graph-a", "graph-b"]);
    expect((await listRuns("graph-a")).map((item) => item.run_id)).toEqual(["run-1", "run-2"]);
    expect(requests).toEqual([
      "http://localhost:8080/graphs?limit=200",
      "http://localhost:8080/graphs?limit=200&cursor=1",
      "http://localhost:8080/graphs/graph-a/runs?limit=500",
      "http://localhost:8080/graphs/graph-a/runs?limit=500&cursor=1",
    ]);
  });

  test("uses exact Graph session and aggregate Run resources", async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = [];
    globalThis.fetch = (async (input, init) => {
      const url = String(input);
      requests.push({ url, init });
      if (url.includes("/sessions") && !url.includes("/runs")) {
        return jsonResponse({
          graph: { id: "graph a", version: "2.0", graph_session_id: "session/1" },
          definition: { nodes: [] },
          settings: runtimeSettings(),
        });
      }
      if (url.endsWith("/runs")) return jsonResponse(run("run-1"), 202);
      return jsonResponse({
        run: run("run-1"),
        steps: [],
        checkpoints: [],
        events: { items: [], next_cursor: "" },
      });
    }) as typeof fetch;

    await createGraphSession("graph a", { nodes: [] }, {}, "2.0");
    await startRun("graph a", "session/1", { shared: {} });
    await getRunInspection("graph a", "run/1");

    expect(requests.map((request) => request.url)).toEqual([
      "http://localhost:8080/graphs/graph%20a/sessions",
      "http://localhost:8080/graphs/graph%20a/sessions/session%2F1/runs",
      "http://localhost:8080/graphs/graph%20a/runs/run%2F1/inspection?event_limit=500",
    ]);
    expect(JSON.parse(String(requests[0].init?.body))).toEqual({
      graph_version: "2.0",
      definition: { nodes: [] },
      settings: {},
    });
  });

  test("replaces all Graph triggers in one request", async () => {
    let request: { url: string; init?: RequestInit } | undefined;
    globalThis.fetch = (async (input, init) => {
      request = { url: String(input), init };
      return jsonResponse([]);
    }) as typeof fetch;

    await replaceTriggers("graph-a", [{ id: "hook", type: "webhook", enabled: true }]);
    expect(request?.url).toBe("http://localhost:8080/graphs/graph-a/triggers");
    expect(request?.init?.method).toBe("PUT");
    expect(JSON.parse(String(request?.init?.body))).toEqual({
      triggers: [{ id: "hook", type: "webhook", enabled: true }],
    });
  });

  test("analyzes Direct Run and current Trigger drafts together", async () => {
    let request: { url: string; init?: RequestInit } | undefined;
    const analysis = {
      direct: { required: [], provided_by_entry: [], provided_by_upstream: [], unresolved: [] },
      triggers: [],
    };
    globalThis.fetch = (async (input, init) => {
      request = { url: String(input), init };
      return jsonResponse(analysis);
    }) as typeof fetch;

    await expect(analyzeInitialStateRequirements(
      "graph a",
      { nodes: [] },
      [{ id: "hook", type: "webhook", webhook: {} }]
    )).resolves.toEqual(analysis);
    expect(request?.url).toBe("http://localhost:8080/graphs/graph%20a/analysis/initial-state-requirements");
    expect(JSON.parse(String(request?.init?.body))).toEqual({
      definition: { nodes: [] },
      triggers: [{ id: "hook", type: "webhook", webhook: {} }],
    });
  });

  test("preserves HTTP status and server error code", async () => {
    globalThis.fetch = (async () => jsonResponse(undefined, 409, {
      code: "trigger_exists",
      message: "trigger already exists",
    })) as unknown as typeof fetch;

    try {
      await replaceTriggers("graph-a", []);
      throw new Error("expected request to fail");
    } catch (error) {
      expect(error).toBeInstanceOf(ApiError);
      expect(error).toMatchObject({ status: 409, code: "trigger_exists", message: "trigger already exists" });
    }
  });

  test("rejects malformed successful responses before they reach callers", async () => {
    globalThis.fetch = (async () => jsonResponse({ items: [{}], next_cursor: "" })) as typeof fetch;

    await expect(listRuns("graph-a")).rejects.toThrow(
      "invalid API response at GET /graphs/graph-a/runs response.items[0].run_id"
    );
  });
});

function jsonResponse(data: unknown, status = 200, error?: { code: string; message: string }): Response {
  return new Response(JSON.stringify(error ? { error } : { data }), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function graph(id: string) {
  return {
    id,
    graph_version: "2.0",
    node_count: 1,
    session_count: 1,
    latest_session: "session-1",
    active_run_count: 0,
    updated_at: "2026-08-07T00:00:00Z",
  };
}

function run(runID: string) {
  return {
    run_id: runID,
    graph_id: "graph-a",
    graph_version: "2.0",
    status: "completed",
    entry_node_id: "start",
    started_at: "2026-08-07T00:00:00Z",
    updated_at: "2026-08-07T00:00:00Z",
  };
}

function runtimeSettings() {
  return { environment: {}, models: [] };
}
