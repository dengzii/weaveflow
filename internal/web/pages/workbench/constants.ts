import type { GraphDefinition } from "../../types";
import { graphDefinitionVersion } from "../../lib/graphEditor";

export const sampleGraph: GraphDefinition = {
  version: graphDefinitionVersion,
  name: "debug_graph",
  state_modules: [{ name: "weaveflow.protocols", version: "1" }],
  entry_point: "input",
  finish_point: "message",
  nodes: [
    {
      id: "input",
      type: "user_input",
      name: "User Input",
      state: {
        value: { path: "shared.request.input" },
        pending_input: { path: "shared.request.pending_input" },
      },
    },
    {
      id: "message",
      type: "conversation_message",
      name: "Conversation Message",
      state: {
        input: { path: "shared.request.input" },
        conversation: { path: "scopes.message.conversation" },
      },
    },
  ],
  edges: [{ from: "input", to: "message" }],
};

export const defaultInitialState = {
  shared: {},
  scopes: {},
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
  "nodes.canceled",
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
