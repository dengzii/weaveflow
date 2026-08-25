import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { RuntimeEvent } from "../../types";
import { RunEventDetail } from "./RunEventDetail";

describe("RunEventDetail", () => {
  test("renders tool execution fields and nested results without expanding raw payload", () => {
    const markup = renderDetail("tool.returned", {
      name: "files.read",
      tool_call_id: "tool-call-1",
      permissions: ["filesystem.read"],
      approval_mode: "on_request",
      operation_key: "operation-1",
      effect_class: "read_only",
      effect_status: "succeeded",
      content: "read complete",
      value: { bytes: 42 },
      duration_ms: 17,
      provider_request_id: "provider-request-1",
      extra_metric: 3,
    });

    expect(markup).toContain(">Tool</span>");
    expect(markup).toContain("files.read");
    expect(markup).toContain(">Operation</span>");
    expect(markup).toContain("operation-1");
    expect(markup).toContain(">Content</div>");
    expect(markup).toContain("read complete");
    expect(markup).toContain(">Result</div>");
    expect(markup).toContain('aria-label="Result JSON tree"');
    expect(markup).toContain(">bytes</span>");
    expect(markup).toContain(">42</span>");
    expect(markup).toContain(">Additional details</div>");
    expect(markup).toContain(">Extra metric</span>");
    expect(markup).toContain("Raw payload");
  });

  test("parses function call JSON string arguments into a collapsible tree", () => {
    const markup = renderDetail("llm.function_call", {
      call_id: "model-call-1",
      tool_call_id: "tool-call-1",
      name: "web.search",
      arguments: '{"query":"weaveflow","options":{"limit":3},"tags":["docs","api"]}',
    });

    expect(markup).toContain('aria-label="Arguments JSON tree"');
    expect(markup).toContain(">query</span>");
    expect(markup).toContain("&quot;weaveflow&quot;");
    expect(markup).toContain("options <span class=\"text-muted-foreground\">Object(1)</span>");
    expect(markup).toContain("tags <span class=\"text-muted-foreground\">Array(2)</span>");
  });

  test("keeps non-JSON function call arguments as plain text", () => {
    const markup = renderDetail("llm.function_call", {
      name: "web.search",
      arguments: "{query: weaveflow}",
    });

    expect(markup).toContain("{query: weaveflow}");
    expect(markup).not.toContain('aria-label="Arguments JSON tree"');
  });

  test("renders condition routing collections and details semantically", () => {
    const markup = renderDetail("condition.evaluated", {
      matched: false,
      targets: ["fallback"],
      sends: [{ target: "worker", correlation_key: "batch-1", order_key: "01" }],
      reason: "fallback route",
      details: { expression: "shared.ready == true" },
    });

    expect(markup).toContain(">Matched</span>");
    expect(markup).toContain(">false</span>");
    expect(markup).toContain(">Targets</span>");
    expect(markup).toContain("fallback");
    expect(markup).toContain(">Sends</div>");
    expect(markup).toContain(">Correlation key</span>");
    expect(markup).toContain(">Details</div>");
    expect(markup).not.toContain(">Additional details</div>");
  });

  test("renders effect outcomes using the effect contract", () => {
    const markup = renderDetail("effect.outcome", {
      key: "operation-2",
      parent_key: "operation-parent",
      kind: "tool",
      name: "files.write",
      class: "idempotent_write",
      status: "succeeded",
      attempt: 2,
      idempotency_key: "write-1",
      provider_request_id: "provider-request-2",
    });

    expect(markup).toContain("operation-2");
    expect(markup).toContain(">Parent operation</span>");
    expect(markup).toContain("idempotent_write");
    expect(markup).toContain(">Effect status</span>");
    expect(markup).not.toContain(">Additional details</div>");
  });

  test("shows custom event extensions as readable additional fields", () => {
    const markup = renderDetail("nodes.custom", {
      event: "provider.progress",
      provider: "codex",
      session_id: "session-1",
      progress: '{"phase":"running","percent":40}',
    });

    expect(markup).toContain("provider.progress");
    expect(markup).toContain(">Provider</span>");
    expect(markup).toContain(">Additional details</div>");
    expect(markup).toContain(">Session ID</span>");
    expect(markup).toContain(">Progress</span>");
    expect(markup).toContain('aria-label="Progress JSON tree"');
    expect(markup).toContain(">percent</span>");
    expect(markup).toContain(">40</span>");
  });
});

function renderDetail(type: string, payload: unknown): string {
  return renderToStaticMarkup(createElement(RunEventDetail, { event: runtimeEvent(type, payload) }));
}

function runtimeEvent(type: string, payload: unknown): RuntimeEvent {
  return {
    id: `event-${type}`,
    graph_session_id: "session-graph",
    run_id: "run-1",
    step_id: "step-1",
    task_id: "task-1",
    node_id: "node-1",
    type,
    timestamp: "2026-08-25T10:00:00Z",
    payload,
  };
}
