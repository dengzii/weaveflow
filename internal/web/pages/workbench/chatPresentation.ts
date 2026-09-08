import type { ChatReply, GraphNodeSpec, RuntimeEvent } from "../../types";

export type ChatNodeKind = "agent" | "model" | "tool" | "reply" | "control" | "node";

export interface ChatNodePresentation {
  id: string;
  label: string;
  type: string;
  kind: ChatNodeKind;
}

export interface ChatReplyGroup {
  node: ChatNodePresentation;
  replies: ChatReply[];
}

export interface ChatNodeActivity {
  id: string;
  node: ChatNodePresentation;
  status: "running" | "completed" | "failed" | "retrying";
  tools: string[];
  events: RuntimeEvent[];
}

export interface ChatExecutionStep extends ChatNodeActivity {
  action: string;
  result: string;
  replies: ChatReply[];
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

export function groupChatReplies(replies: ChatReply[], nodes: GraphNodeSpec[]): ChatReplyGroup[] {
  const groups: ChatReplyGroup[] = [];
  for (const reply of replies.filter((item) => item.kind === "message" && item.content?.trim())) {
    const node = chatNodePresentation(reply.node_id, nodes);
    const previous = groups.at(-1);
    if (previous?.node.id === node.id) previous.replies.push(reply);
    else groups.push({ node, replies: [reply] });
  }
  return groups;
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
  const replyGroups = groupChatReplies(replies, nodes);
  const steps = buildChatNodeActivities(events, nodes).map((activity): ChatExecutionStep => ({
    ...activity,
    action: activityAction(activity),
    result: activityResult(activity),
    replies: [],
  }));
  for (const group of replyGroups) {
    const step = [...steps].reverse().find((candidate) => candidate.node.id === group.node.id);
    if (step) {
      step.replies.push(...group.replies);
      continue;
    }
    steps.push({
      id: `reply:${group.node.id}:${steps.length}`,
      node: group.node,
      status: "completed",
      tools: [],
      events: [],
      action: "Generated a response",
      result: "Response ready",
      replies: group.replies,
    });
  }
  return steps;
}

function activityAction(activity: ChatNodeActivity): string {
  const last = activity.events.at(-1);
  if (!last) return "Working";
  if (last.type === "nodes.retry") return "Retrying this step";
  if (last.type === "nodes.failed" || last.type === "nodes.canceled") return "Step stopped";
  if (last.type === "nodes.finished") return "Step completed";
  if (last.type === "tool.approval_needed") return "Waiting for tool approval";
  if (last.type === "tool.called" || last.type === "tool.started") return activity.tools.length > 0 ? `Using ${activity.tools.join(", ")}` : "Using tools";
  if (last.type === "tool.returned") return activity.tools.length > 0 ? `Processed ${activity.tools.join(", ")}` : "Processing tool results";
  if (last.type === "llm.reasoning" || last.type === "llm.reasoning_chunk") return "Thinking through the request";
  if (last.type.startsWith("llm.")) return "Generating a response";
  if (activity.node.kind === "tool") return "Running tools";
  if (activity.node.kind === "agent") return "Working on the task";
  if (activity.node.kind === "control") return "Preparing context";
  return "Running this step";
}

function activityResult(activity: ChatNodeActivity): string {
  if (activity.status === "failed") {
    const failure = [...activity.events].reverse().find((event) => event.type === "nodes.failed" || event.type === "tool.failed");
    return payloadString(failure?.payload, "error") || "Step failed";
  }
  if (activity.status === "retrying") return "A retry was scheduled";
  if (activity.status !== "completed") return "";
  if (activity.tools.length > 0) return `Used ${activity.tools.join(", ")}`;
  return "Completed successfully";
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
