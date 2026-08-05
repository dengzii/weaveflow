import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { CheckpointDetail, RunRecord, RuntimeEvent } from "../../types";
import {
  RunStatusPanel,
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
});

function runRecord(runID: string, status: string, startedAt: string): RunRecord {
  return {
    run_id: runID,
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

function runStatusMarkup(markup: string, status: string): string {
  const start = markup.indexOf(`data-run-status="${status}"`);
  const end = markup.indexOf("</span>", start);
  return markup.slice(start, end);
}
