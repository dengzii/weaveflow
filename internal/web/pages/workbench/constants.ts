import type { GraphDefinition } from "../../types";

export const workspaceTabs = ["graph", "triggers", "settings"] as const;
export type WorkspaceTab = (typeof workspaceTabs)[number];

export const sampleGraph: GraphDefinition = {
  version: "2.0",
  name: "debug_graph",
  state_modules: [{ name: "weaveflow.protocols", version: "1" }],
  entry_point: "input",
  finish_point: "input",
  nodes: [
    {
      id: "input",
      type: "conversation_input",
      name: "Input",
      config: { content: "hello" },
      state: {
        input: { path: "shared.request.input" },
        conversation: { path: "shared.conversation" },
      },
    },
  ],
};

export const defaultInitialState = {
  shared: {},
  scopes: {},
  internal: {},
  runtime: {},
};

export const runtimeEventTypes = [
  "run.created",
  "run.started",
  "run.pause_requested",
  "run.paused",
  "run.resumed",
  "run.cancel_requested",
  "run.canceled",
  "run.finished",
  "run.failed",
  "nodes.started",
  "nodes.finished",
  "nodes.failed",
  "nodes.retry",
  "nodes.custom",
  "llm.reasoning_chunk",
  "llm.content_chunk",
  "llm.reasoning",
  "llm.content",
  "llm.function_call",
  "llm.usage",
  "llm.call",
  "tool.started",
  "tool.called",
  "tool.returned",
  "tool.failed",
  "subgraph.started",
  "subgraph.finished",
  "subgraph.failed",
  "checkpoint.created",
  "artifact.created",
  "breakpoint.hit",
  "state.changed",
  "contract.violation",
  "warning",
];

export const extensionPoints = [
  "Node palette",
  "Condition builder",
  "Runner profiles",
  "Artifact viewers",
  "Schema forms",
  "Layout plugins",
];
