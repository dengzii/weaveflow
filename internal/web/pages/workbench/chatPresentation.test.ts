import { describe, expect, test } from "bun:test";
import type { ChatReply, GraphNodeSpec, RuntimeEvent } from "../../types";
import { buildChatExecutionSteps, buildChatNodeActivities, chatNodePresentation, groupChatReplies } from "./chatPresentation";

const nodes: GraphNodeSpec[] = [
  { id: "research_agent", name: "Research agent", type: "agent" },
  { id: "run_tools", name: "Run tools", type: "tool_execution" },
  { id: "answer", name: "Final answer", type: "chat_reply" },
];

describe("chat presentation", () => {
  test("uses graph node metadata to classify assistant messages", () => {
    expect(chatNodePresentation("research_agent", nodes)).toMatchObject({ label: "Research agent", kind: "agent" });
    expect(chatNodePresentation("answer", nodes)).toMatchObject({ label: "Final answer", kind: "reply" });
  });

  test("groups adjacent messages by producing node", () => {
    const replies: ChatReply[] = [
      { kind: "message", node_id: "research_agent", content: "First", sequence: 1 },
      { kind: "message", node_id: "research_agent", content: "Second", sequence: 2 },
      { kind: "message", node_id: "answer", content: "Done", sequence: 3 },
    ];
    const groups = groupChatReplies(replies, nodes);
    expect(groups.map((group) => [group.node.id, group.replies.length])).toEqual([
      ["research_agent", 2],
      ["answer", 1],
    ]);
  });

  test("summarizes node status and tool activity from runtime events", () => {
    const events: RuntimeEvent[] = [
      event("1", "nodes.started", "run_tools", {}),
      event("2", "tool.called", "run_tools", { tools: [{ name: "search" }, { name: "fetch" }] }),
      event("3", "tool.returned", "run_tools", { name: "search" }),
      event("4", "nodes.finished", "run_tools", {}),
    ];
    expect(buildChatNodeActivities(events, nodes)).toMatchObject([{
      node: { label: "Run tools", kind: "tool" },
      status: "completed",
      tools: ["search", "fetch"],
      events,
    }]);
  });

  test("turns runtime events into readable execution steps and attaches node replies", () => {
    const events: RuntimeEvent[] = [
      { ...event("1", "nodes.started", "research_agent", {}), step_id: "step-1" },
      { ...event("2", "llm.reasoning_chunk", "research_agent", { text: "thinking" }), step_id: "step-1" },
      { ...event("3", "nodes.finished", "research_agent", {}), step_id: "step-1" },
    ];
    const steps = buildChatExecutionSteps(events, [{ kind: "message", node_id: "research_agent", content: "Result" }], nodes);
    expect(steps).toMatchObject([{
      id: "step-1",
      node: { label: "Research agent" },
      status: "completed",
      action: "Step completed",
      result: "Completed successfully",
      replies: [{ content: "Result" }],
    }]);
  });
});

function event(id: string, type: string, nodeID: string, payload: unknown): RuntimeEvent {
  return { id, run_id: "run-1", node_id: nodeID, type, timestamp: `2026-09-08T00:00:0${id}Z`, payload };
}
