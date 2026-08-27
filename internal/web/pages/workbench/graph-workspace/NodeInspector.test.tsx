import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { GraphNodeSpec, RuntimeEvent, StepRecord } from "../../../types";
import { NodeRuntimeInspector } from "./NodeInspector";

const selectedNode: GraphNodeSpec = { id: "task", type: "task" };

describe("NodeRuntimeInspector", () => {
  test("renders current runtime from the run update before Steps arrive", () => {
    const runUpdatedAt = new Date(Date.now() - 2_000).toISOString();
    const markup = renderToStaticMarkup(createElement(NodeRuntimeInspector, {
      selectedNode,
      selectedRunID: "run-1",
      currentNodeIDs: [selectedNode.id],
      runStatus: "running",
      runUpdatedAt,
      steps: [],
    }));

    expect(markup).toContain("running/current/");
    expect(markup).not.toContain(">0ms<");
  });

  test("renders cumulative and current-attempt runtime after retry", () => {
    const startedAt = Date.now() - 5_500;
    const steps: StepRecord[] = [{
      step_id: "step-1",
      run_id: "run-1",
      task_id: "task-1",
      node_id: selectedNode.id,
      node_name: "Task",
      status: "running",
      attempt: 1,
      started_at: new Date(startedAt).toISOString(),
      updated_at: new Date(startedAt).toISOString(),
    }];
    const events: RuntimeEvent[] = [
      {
        id: "retry",
        run_id: "run-1",
        step_id: "step-1",
        node_id: selectedNode.id,
        type: "nodes.retry",
        timestamp: new Date(startedAt + 2_500).toISOString(),
        payload: { attempt: 1, next_attempt: 2, delay: "1s" },
      },
    ];
    const markup = renderToStaticMarkup(createElement(NodeRuntimeInspector, {
      selectedNode,
      selectedRunID: "run-1",
      currentNodeIDs: [selectedNode.id],
      runStatus: "running",
      runUpdatedAt: new Date(startedAt).toISOString(),
      steps,
      events,
    }));

    expect(markup).toContain("running/current/2.0s");
    expect(markup).toContain(">4.5s<");
  });
});
