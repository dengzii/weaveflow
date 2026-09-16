import type { ChatReply, GraphNodeSpec, RuntimeEvent } from "../../types";

export type ChatNodeKind = "agent" | "model" | "tool" | "reply" | "control" | "node";

export interface ChatNodePresentation {
  id: string;
  label: string;
  type: string;
  kind: ChatNodeKind;
}

export interface ChatNodeActivity {
  id: string;
  node: ChatNodePresentation;
  status: "running" | "completed" | "failed" | "retrying";
  tools: string[];
  events: RuntimeEvent[];
}

export interface ChatExecutionStep extends ChatNodeActivity {
  kind: "activity" | "message";
  action: string;
  result: string;
  content: string;
  replyKind?: ChatReply["kind"];
}

export function chatNodePresentation(nodeID: string | undefined, nodes: GraphNodeSpec[]): ChatNodePresentation {
  const id = nodeID?.trim() || "assistant";
  const node = nodes.find((candidate) => candidate.id === id);
  const type = node?.type?.trim() || "node";
  return {
    id,
    label: node?.name?.trim() || humanizeIdentifier(id),
    type,
    kind: nodeKind(type),
  };
}

export function buildChatNodeActivities(events: RuntimeEvent[], nodes: GraphNodeSpec[]): ChatNodeActivity[] {
  const activities = new Map<string, ChatNodeActivity>();
  for (const event of events) {
    const nodeID = event.node_id?.trim();
    if (!nodeID) continue;
    const activityID = event.step_id?.trim() || nodeID;
    let activity = activities.get(activityID);
    if (!activity) {
      activity = {
        id: activityID,
        node: chatNodePresentation(nodeID, nodes),
        status: "running",
        tools: [],
        events: [],
      };
      activities.set(activityID, activity);
    }
    activity.events.push(event);
    if (event.type === "nodes.finished") activity.status = "completed";
    else if (event.type === "nodes.failed" || event.type === "nodes.canceled") activity.status = "failed";
    else if (event.type === "nodes.retry") activity.status = "retrying";
    else if (event.type === "nodes.started") activity.status = "running";
    if (event.type.startsWith("tool.")) {
      const name = payloadString(event.payload, "name");
      if (name && !activity.tools.includes(name)) activity.tools.push(name);
      for (const tool of payloadTools(event.payload)) {
        if (!activity.tools.includes(tool)) activity.tools.push(tool);
      }
    }
  }
  return [...activities.values()];
}

export function buildChatExecutionSteps(events: RuntimeEvent[], replies: ChatReply[], nodes: GraphNodeSpec[]): ChatExecutionStep[] {
  const steps = buildChatNodeActivities(events, nodes).map((activity): ChatExecutionStep => ({
    ...activity,
    kind: "activity",
    action: activityAction(activity),
    result: activityResult(activity),
    content: "",
  }));

  for (const [replyIndex, reply] of replies.entries()) {
    const content = reply.content?.trim();
    if (!content || (reply.kind !== "message" && reply.kind !== "finish")) continue;
    const node = replyPresentation(reply, nodes);
    const replyStep: ChatExecutionStep = {
      id: `reply:${reply.sequence ?? replyIndex}:${node.id}:${reply.kind}`,
      node,
      status: "completed",
      tools: [],
      events: [],
      kind: "message",
      action: reply.kind === "finish" ? "Final answer" : "Message",
      result: "",
      content,
      replyKind: reply.kind,
    };
    const nodeStepIndex = lastNodeStepIndex(steps, node.id);
    if (nodeStepIndex < 0) {
      steps.push(replyStep);
      continue;
    }
    let insertionIndex = nodeStepIndex + 1;
    while (insertionIndex < steps.length && steps[insertionIndex].kind === "message" && steps[insertionIndex].node.id === node.id) {
      insertionIndex++;
    }
    steps.splice(insertionIndex, 0, replyStep);
  }
  return steps;
}

function lastNodeStepIndex(steps: ChatExecutionStep[], nodeID: string): number {
  for (let index = steps.length - 1; index >= 0; index--) {
    if (steps[index].node.id === nodeID) return index;
  }
  return -1;
}

function replyPresentation(reply: ChatReply, nodes: GraphNodeSpec[]): ChatNodePresentation {
  const node = chatNodePresentation(reply.node_id, nodes);
  if (reply.kind !== "finish" || reply.node_id?.trim()) return node;
  return { ...node, label: "Final answer", type: "finish", kind: "reply" };
}

function activityAction(activity: ChatNodeActivity): string {
  const last = activity.events.at(-1);
  if (!last) return "Running";
  if (last.type === "nodes.retry") return "Retrying";
  if (last.type === "nodes.failed") return "Failed";
  if (last.type === "nodes.canceled") return "Canceled";
  if (last.type === "nodes.finished") return toolAction("Completed", activity.tools);
  if (last.type === "tool.approval_needed") return toolAction("Approval", activity.tools);
  if (last.type === "tool.called" || last.type === "tool.started") return toolAction("Running", activity.tools);
  if (last.type === "tool.returned") return toolAction("Returned", activity.tools);
  if (last.type === "tool.failed") return toolAction("Failed", activity.tools);
  if (last.type === "llm.reasoning" || last.type === "llm.reasoning_chunk") return "Reasoning";
  if (last.type.startsWith("llm.")) return "Generating";
  if (activity.node.kind === "control") return "Routing";
  return "Running";
}

function activityResult(activity: ChatNodeActivity): string {
  if (activity.status === "failed") {
    const failure = [...activity.events].reverse().find((event) => event.type === "nodes.failed" || event.type === "tool.failed");
    return payloadString(failure?.payload, "error") || "Failed";
  }
  if (activity.status === "retrying") return "Retry scheduled";
  return "";
}

function toolAction(action: string, tools: string[]): string {
  return tools.length > 0 ? `${action} · ${tools.join(", ")}` : action;
}

function nodeKind(type: string): ChatNodeKind {
  const normalized = type.toLowerCase();
  if (normalized === "chat_reply" || normalized.includes("final_answer") || normalized.includes("synthesis")) return "reply";
  if (normalized.includes("tool")) return "tool";
  if (normalized.includes("agent") || normalized.includes("supervisor") || normalized.includes("plan")) return "agent";
  if (normalized.includes("llm") || normalized.includes("generation")) return "model";
  if (normalized.startsWith("state_") || normalized.includes("route") || normalized.includes("condition")) return "control";
  return "node";
}

function humanizeIdentifier(value: string): string {
  return value.replace(/[._-]+/g, " ").replace(/\b\w/g, (character) => character.toUpperCase());
}

function payloadString(payload: unknown, key: string): string {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return "";
  const value = (payload as Record<string, unknown>)[key];
  return typeof value === "string" ? value.trim() : "";
}

function payloadTools(payload: unknown): string[] {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return [];
  const tools = (payload as Record<string, unknown>).tools;
  if (!Array.isArray(tools)) return [];
  return tools.flatMap((tool) => {
    if (!tool || typeof tool !== "object" || Array.isArray(tool)) return [];
    const name = (tool as Record<string, unknown>).name;
    return typeof name === "string" && name.trim() ? [name.trim()] : [];
  });
}
