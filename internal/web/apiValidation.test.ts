import { describe, expect, test } from "bun:test";
import {
  validateGraphDetail,
  validateGraphListPage,
  validateGraphLoadResult,
  validateRegistryInfo,
  validateRunInspection,
  validateRunListPage,
  validateRunRecord,
  validateRunResult,
  validateRuntimeEventPage,
  validateToolsInfo,
} from "./apiValidation";

describe("API response validation", () => {
  test("accepts core Graph and Run response shapes", () => {
    expect(validateGraphListPage({ items: [graphSummary()], next_cursor: "" }, "graphs")).toEqual({
      items: [graphSummary()],
      next_cursor: "",
    });
    expect(validateGraphDetail(graphDetail(), "graph detail")).toEqual(graphDetail());
    expect(validateGraphLoadResult({
      graph: graphInfo(),
      definition: graphDefinition(),
      settings: runtimeSettings(),
    }, "graph session")).toMatchObject({ graph: graphInfo() });

    expect(validateRunListPage({ items: [runRecord()], next_cursor: "" }, "runs").items).toHaveLength(1);
    expect(validateRunRecord(runRecord(), "run")).toEqual(runRecord());
    expect(validateRunResult({ run: runRecord(), state: {} }, "run result")).toMatchObject({ run: runRecord() });
    expect(validateRunInspection(runInspection(), "inspection")).toEqual(runInspection());
    expect(validateRunInspection({ ...runInspection(), steps: [canceledStep()] }, "inspection").steps[0]?.status)
      .toBe("canceled");
  });

  test("accepts runtime event, Registry, and Tool responses", () => {
    const eventPage = {
      items: [{ id: "event-1", run_id: "run-1", type: "run.started", timestamp: timestamp }],
      next_cursor: "cursor-1",
    };
    expect(validateRuntimeEventPage(eventPage, "events")).toEqual(eventPage);
    expect(validateRegistryInfo({
      state_modules: [],
      capabilities: [],
      node_groups: [],
      node_types: [{ type: "user_input" }],
      conditions: [{ type: "always" }],
      graph_schema: {},
    }, "registry")).toMatchObject({ node_types: [{ type: "user_input" }] });
    expect(validateToolsInfo({ tools: [{ id: "calculator" }] }, "tools")).toEqual({
      tools: [{ id: "calculator" }],
    });
  });

  test("rejects malformed nested data with its response path", () => {
    expect(() => validateGraphListPage({ items: [{ ...graphSummary(), node_count: "1" }], next_cursor: "" }, "graphs"))
      .toThrow("invalid API response at graphs.items[0].node_count: expected a finite number");
    expect(() => validateRunInspection({ ...runInspection(), events: { items: [{}], next_cursor: "" } }, "inspection"))
      .toThrow("invalid API response at inspection.events.items[0].id: expected a non-empty string");
    expect(() => validateToolsInfo({ tools: [{ name: "calculator" }] }, "tools"))
      .toThrow("invalid API response at tools.tools[0].id: expected a non-empty string");
  });
});

const timestamp = "2026-08-09T00:00:00Z";

function graphSummary() {
  return {
    id: "graph-1",
    graph_version: "2.0",
    node_count: 1,
    session_count: 1,
    latest_session: "session-1",
    active_run_count: 0,
    updated_at: timestamp,
  };
}

function graphInfo() {
  return { id: "graph-1", version: "2.0", graph_session_id: "session-1" };
}

function graphDefinition() {
  return {
    version: "1.0",
    state_modules: [{ name: "weaveflow.protocols", version: "1" }],
    nodes: [{ id: "start", type: "user_input" }],
  };
}

function runtimeSettings() {
  return { environment: {}, environment_secrets: {}, models: [], tool_permissions: [], tool_approvals: {} };
}

function graphDetail() {
  return {
    graph: graphInfo(),
    definition: graphDefinition(),
    settings: runtimeSettings(),
    initial_state_requirements: {
      required: [],
      provided_by_entry: [],
      provided_by_upstream: [],
      unresolved: [],
    },
    latest_session: { id: "session-1", created_at: timestamp },
    active: { active_run_count: 0 },
  };
}

function runRecord() {
  return {
    run_id: "run-1",
    revision: 1,
    root_run_id: "run-1",
    run_path: ["run-1"],
    namespace: "run-1",
    graph_id: "graph-1",
    graph_version: "2.0",
    status: "running",
    entry_node_id: "start",
    started_at: timestamp,
    updated_at: timestamp,
  };
}

function runInspection() {
  return {
    run: runRecord(),
    steps: [],
    checkpoints: [],
    events: { items: [], next_cursor: "" },
  };
}

function canceledStep() {
  return {
    step_id: "step-1",
    run_id: "run-1",
    task_id: "start",
    node_id: "start",
    node_name: "Start",
    status: "canceled",
    attempt: 1,
    started_at: timestamp,
    updated_at: timestamp,
  };
}
