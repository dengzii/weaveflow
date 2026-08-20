import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { CheckpointDetail, RunComparison, RunRecord, RunStatus, RuntimeEvent } from "../../types";
import {
  RunStatusPanel,
  RunMetrics,
  RunInputOutput,
  RunOverview,
  RunComparisonDetail,
  RunPanelTabs,
  StateDetailTabs,
  StateSnapshotDetail,
  resizeRunPanelColumnRatios,
} from "./RunStatusPanel";

describe("RunStatusPanel", () => {
  test("renders resizable columns with the default 1 to 1.5 to 2 ratio", () => {
    const markup = renderToStaticMarkup(
      createElement(RunStatusPanel, {
        runs: [runRecord("run-new", "running", "2026-07-30T02:00:00Z")],
        selectedRunId: "run-new",
        onSelectRun: () => undefined,
        onDeleteRun: () => undefined,
        events: [runtimeEvent(), stateChangeEvent()],
        hasOlderEvents: true,
        onLoadOlderEvents: () => undefined,
        onHide: () => undefined,
      })
    );

    expect(markup).toContain(
      "grid-template-columns:minmax(0, 1fr) 1px minmax(0, 1.5fr) 1px minmax(0, 2fr)"
    );
    expect(markup).toContain('aria-label="Resize Run and Event columns"');
    expect(markup).toContain('aria-label="Resize Event and Event Detail columns"');
    expect(markup).toContain('aria-label="Run list"');
    expect(markup).toContain('aria-label="Event list"');
    expect(markup).toContain('aria-label="Event detail"');
    expect(markup).toContain('aria-label="Run history view"');
    expect(markup).toContain('role="tab" aria-selected="true"');
    expect(markup).toContain(">Overview</button>");
    expect(markup).toContain(">Input / Output</button>");
    expect(markup).toContain(">Metrics</button>");
    expect(markup).toContain(">Event</button>");
    expect(markup).toContain(">State</button>");
    expect(markup).not.toContain("data-state-history-count");
    expect(markup).not.toContain("Run Event");
    expect(markup).not.toContain("State History");
    expect(markup).toContain("position:absolute");
    expect(markup).toContain("height:28px");
    expect(markup).toContain("Load older");
    expect(markup).toContain("relative z-20 isolate flex min-h-0 shrink-0 flex-col overflow-hidden bg-panel");
    expect(markup).not.toContain("content-visibility:auto");
    expect(markup.indexOf('aria-label="Run history view"')).toBeLessThan(markup.indexOf('aria-label="Filter events"'));
  });

  test("resizes adjacent columns while preserving the total ratio", () => {
    const resized = resizeRunPanelColumnRatios([1, 1.5, 2], 0, 30, 900);

    expect(resized[0]).toBeCloseTo(1.15);
    expect(resized[1]).toBeCloseTo(1.35);
    expect(resized[2]).toBeCloseTo(2);
    expect(resized[0] + resized[1] + resized[2]).toBeCloseTo(4.5);
  });

  test("renders selected run overview and metrics content", () => {
    const run = {
      ...runRecord("run-overview", "completed", "2026-07-30T02:00:00Z"),
      current_node_id: "review",
      finished_at: "2026-07-30T02:00:05Z",
      return_value: { answer: "ready" },
    };
    const metrics = {
      durationMs: 5_000,
      eventCount: 12,
      stepCount: 3,
      succeededSteps: 3,
      failedSteps: 0,
      activeSteps: 0,
      retries: 1,
      checkpointCount: 4,
      stateChangeCount: 7,
      llmCallCount: 2,
      toolCallCount: 3,
      toolFailureCount: 1,
      promptTokens: 100,
      completionTokens: 20,
      reasoningTokens: 5,
      cachedPromptTokens: 40,
      warningCount: 1,
      errorCount: 1,
    };
    const tabs = renderToStaticMarkup(
      createElement(RunPanelTabs, { view: "metrics", onChange: () => undefined })
    );
    const inputDetail = checkpointDetail();
    const outputDetail = {
      ...checkpointDetail(),
      record: { ...checkpointDetail().record, checkpoint_id: "checkpoint-final", stage: "final" },
      artifacts: [{ id: "artifact-1", type: "report" }],
    };
    const overview = renderToStaticMarkup(createElement(RunOverview, {
      run,
      metrics,
    }));
    const inputOutput = renderToStaticMarkup(createElement(RunInputOutput, {
      run,
      inputCheckpoint: inputDetail.record,
      outputCheckpoint: outputDetail.record,
      inputDetail,
      outputDetail,
    }));
    const metricMarkup = renderToStaticMarkup(createElement(RunMetrics, { metrics, partial: true }));

    expect(tabs).toContain('role="tab" aria-selected="true"');
    expect(tabs).toContain(">Metrics</button>");
    expect(overview).toContain('data-run-overview="run-overview"');
    expect(overview).toContain("Current node");
    expect(overview).toContain("review");
    expect(overview).not.toContain("Input / Output");
    expect(overview).not.toContain("Open Input / Output");
    expect(inputOutput).toContain('data-run-input-output="run-overview"');
    expect(inputOutput).toContain("grid h-full min-h-0 gap-3 lg:grid-cols-2");
    expect(inputOutput).toContain("h-full min-w-0 overflow-auto");
    expect(inputOutput).toContain(">Input</span>");
    expect(inputOutput).toContain(">Output</span>");
    expect(inputOutput).toContain("07-30 02:00:02.000");
    expect(inputOutput).not.toContain("checkpoint-after");
    expect(inputOutput).not.toContain("checkpoint-final");
    expect(inputOutput).not.toContain("after_node");
    expect(inputOutput).toContain("artifact-1");
    expect(inputOutput).toContain('aria-label="Input JSON view"');
    expect(inputOutput).toContain('aria-label="Search input JSON"');
    expect(inputOutput).toContain('aria-label="Input JSON tree"');
    expect(inputOutput).toContain(">tree</button>");
    expect(inputOutput).toContain(">raw</button>");
    expect(inputOutput).toContain('aria-label="Expand all input nodes"');
    expect(inputOutput).toContain("lucide-unfold-vertical");
    expect(inputOutput.indexOf('aria-label="Search input JSON"')).toBeLessThan(
      inputOutput.indexOf('aria-label="Input JSON view"')
    );
    expect(inputOutput).toContain(">return_value <span");
    expect(inputOutput).toContain(">shared <span");
    expect(inputOutput).not.toContain(">internal <span");
    expect(metricMarkup).toContain('data-run-metrics="true"');
    expect(metricMarkup).toContain("Model usage");
    expect(metricMarkup).toContain("Total tokens");
    expect(metricMarkup).toContain(">125<");
    expect(metricMarkup).toContain("loaded events only");
  });

  test("renders Snapshot as the default State detail with complete State sections", () => {
    const tabs = renderToStaticMarkup(
      createElement(StateDetailTabs, { view: "snapshot", onChange: () => undefined })
    );
    const detail = renderToStaticMarkup(
      createElement(StateSnapshotDetail, { detail: checkpointDetail() })
    );

    expect(tabs).toContain('aria-label="State detail view"');
    expect(tabs).toContain('role="tab" aria-selected="false"');
    expect(tabs).toContain(">Diff</button>");
    expect(tabs).toContain('role="tab" aria-selected="true"');
    expect(tabs).toContain(">Snapshot</button>");
    expect(detail).toContain(">shared</span>");
    expect(detail).toContain(">scopes</span>");
    expect(detail).toContain(">internal</span>");
    expect(detail).toContain(">runtime</span>");
    expect(detail).toContain('aria-label="shared state snapshot"');
    expect(detail).toContain('aria-label="scopes state snapshot"');
    expect(detail).not.toContain('aria-label="internal state snapshot"');
    expect(detail).not.toContain('aria-label="runtime state snapshot"');
    expect(detail).toContain("Preparing snapshot…");
    expect(detail).not.toContain("ready");
    expect(detail).toContain("overflow-wrap:anywhere");
  });

  test("renders direct, chat, and webhook run sources as icons", () => {
    const markup = renderToStaticMarkup(
      createElement(RunStatusPanel, {
        runs: [
          runRecord("run-webhook", "failed", "2026-07-30T01:00:00Z"),
          runRecord("run-chat", "paused", "2026-07-30T02:00:00Z"),
          runRecord("run-direct", "running", "2026-07-30T03:00:00Z"),
        ],
        runTriggerTypes: { "run-webhook": "webhook", "run-chat": "chat" },
        selectedRunId: "run-direct",
        onDeleteRun: () => undefined,
        events: [],
        onHide: () => undefined,
      })
    );

    expect(markup).toContain('data-run-source="webhook"');
    expect(markup).toContain('aria-label="Webhook"');
    expect(markup).toContain("lucide-webhook");
    expect(markup).toContain('data-run-source="chat"');
    expect(markup).toContain('aria-label="Chat"');
    expect(markup).toContain("lucide-message-circle");
    expect(markup).toContain('data-run-source="direct"');
    expect(markup).toContain('aria-label="Run"');
    expect(markup).toContain("lucide-play");
    expect(markup).toContain('data-run-status="failed"');
    expect(markup).toContain('data-run-status="paused"');
    expect(markup).toContain('data-run-status="running"');

    const sourceIndex = markup.indexOf('data-run-source="direct"');
    const runIDIndex = markup.indexOf('title="run-direct"');
    const deleteIndex = markup.indexOf('aria-label="Delete run run-direct"');
    const statusIndex = markup.indexOf('data-run-status="running"');
    expect(sourceIndex).toBeLessThan(runIDIndex);
    expect(sourceIndex).toBeLessThan(statusIndex);
    expect(statusIndex).toBeLessThan(runIDIndex);
    expect(runIDIndex).toBeLessThan(deleteIndex);
    expect(runStatusMarkup(markup, "running")).toContain("<svg");
  });

  test("disables run deletion while another run operation is in progress", () => {
    const markup = renderToStaticMarkup(
      createElement(RunStatusPanel, {
        runs: [runRecord("run-completed", "completed", "2026-07-30T03:00:00Z")],
        selectedRunId: "run-completed",
        runActionsDisabled: true,
        onDeleteRun: () => undefined,
        events: [],
        onHide: () => undefined,
      })
    );

    expect(markup).toContain('title="Run operation in progress"');
    expect(markup).toContain('disabled="" title="Run operation in progress" aria-label="Delete run run-completed"');
  });

  test("keeps the event scroll viewport mounted before events arrive", () => {
    const markup = renderToStaticMarkup(
      createElement(RunStatusPanel, {
        runs: [runRecord("run-pending", "pending", "2026-07-30T03:00:00Z")],
        selectedRunId: "run-pending",
        events: [],
        onHide: () => undefined,
      })
    );

    expect(markup).toContain('data-event-history-viewport="true"');
    expect(markup).toContain('class="h-full overflow-auto"');
    expect(markup).toContain("No run events");
  });

  test("uses stable loading content while a selected run inspection is pending", () => {
    const markup = renderToStaticMarkup(
      createElement(RunStatusPanel, {
        runs: [runRecord("run-loading", "running", "2026-07-30T03:00:00Z")],
        selectedRunId: "run-loading",
        runInspectionLoading: true,
        events: [],
        onHide: () => undefined,
      })
    );

    expect(markup).toContain("Loading events…");
    expect(markup).toContain("Loading event detail…");
    expect(markup).not.toContain("No run events");
    expect(markup).not.toContain("Select an event");
  });

  test("exposes fork and compare controls with checkpoint navigation", () => {
    const source = { ...runRecord("run-source", "paused", "2026-07-30T03:00:00Z"), last_checkpoint_id: "checkpoint-source" };
    const comparison: RunComparison = {
      left: source,
      right: runRecord("run-fork", "completed", "2026-07-30T03:01:00Z"),
      left_steps: [],
      right_steps: [],
      left_events: [],
      right_events: [],
      left_artifacts: [],
      right_artifacts: [],
      state_changes: [{ path: "shared.value", before: "source", after: "fork" }],
      checkpoint_id: "checkpoint-source",
      other_checkpoint_id: "checkpoint-fork",
    };
    const markup = renderToStaticMarkup(
      createElement(RunStatusPanel, {
        runs: [source, comparison.right],
        selectedRunId: source.run_id,
        onForkRun: () => undefined,
        onCompareRuns: () => undefined,
        runComparison: comparison,
        events: [],
        onHide: () => undefined,
      })
    );
    const comparisonMarkup = renderToStaticMarkup(
      createElement(RunComparisonDetail, { comparison, loading: false, onSelectCheckpoint: () => undefined })
    );

    expect(markup).toContain(">Fork</button>");
    expect(markup).toContain('aria-label="Compare selected run with"');
    expect(markup).toContain(">Compare</button>");
    expect(comparisonMarkup).toContain('data-run-comparison="true"');
    expect(comparisonMarkup).toContain("checkpoint-source");
    expect(comparisonMarkup).toContain("shared.value");
  });
});

function runRecord(runID: string, status: RunStatus, startedAt: string): RunRecord {
  return {
    run_id: runID,
    revision: 1,
    root_run_id: runID,
    run_path: [runID],
    namespace: runID,
    graph_id: "graph",
    graph_version: "1.0",
    status,
    entry_node_id: "input",
    started_at: startedAt,
    updated_at: startedAt,
  };
}

function runtimeEvent(): RuntimeEvent {
  return {
    id: "event-1",
    run_id: "run-new",
    node_id: "llm",
    type: "nodes.started",
    timestamp: "2026-07-30T02:00:01Z",
  };
}

function stateChangeEvent(): RuntimeEvent {
  return {
    id: "state-1",
    run_id: "run-new",
    step_id: "step-1",
    node_id: "llm",
    type: "state.changed",
    timestamp: "2026-07-30T02:00:02Z",
    payload: {
      changes: [
        { path: "shared.answer", after: "ready" },
        { path: "shared.count", before: 1, after: 2 },
      ],
    },
  };
}

function checkpointDetail(): CheckpointDetail {
  return {
    record: {
      checkpoint_id: "checkpoint-after",
      run_id: "run-new",
      step_id: "step-1",
      node_id: "llm",
      stage: "after_node",
      state_codec: "json",
      state_version: "state-v2",
      created_at: "2026-07-30T02:00:02Z",
    },
    snapshot: {
      version: "state-v2",
      shared: { answer: "ready" },
      scopes: { llm: { messages: [] } },
      internal: { retries: 0 },
      runtime: { run_id: "run-new" },
    },
  };
}

function runStatusMarkup(markup: string, status: RunStatus): string {
  const start = markup.indexOf(`data-run-status="${status}"`);
  const end = markup.indexOf("</span>", start);
  return markup.slice(start, end);
}
