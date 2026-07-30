import { START_NODE_REF, graphNodePositions, type NodePosition } from "../../../lib/graphEditor";
import type { GraphDefinition, Trigger, TriggerCanvasNode } from "../../../types";

const triggerCanvasPrefix = "__weaveflow_trigger__:";

export function triggerCanvasNodeID(triggerID: string, usedIDs: Set<string> = new Set()): string {
  const base = `${triggerCanvasPrefix}${encodeURIComponent(triggerID)}`;
  let candidate = base;
  for (let suffix = 2; usedIDs.has(candidate); suffix += 1) candidate = `${base}:${suffix}`;
  return candidate;
}

export function projectTriggerCanvasNodes(
  definition: GraphDefinition | null,
  graphID: string,
  triggers: Trigger[],
  virtualNodeIDs: string[]
): TriggerCanvasNode[] {
  if (!definition || !graphID.trim()) return [];
  const matched = triggers
    .filter((trigger) => trigger.target?.graph_id?.trim() === graphID.trim())
    .sort((left, right) => left.id.localeCompare(right.id));
  const stored = triggerNodePositions(definition);
  const positions = graphNodePositions(definition);
  const startID = virtualNodeIDs.find((nodeID) => nodeID === START_NODE_REF || nodeID.startsWith(`${START_NODE_REF}:`));
  const anchor = (startID ? positions.get(startID) : undefined)
    ?? (startID ? { x: -260, y: 0 } : undefined)
    ?? (definition.entry_point ? positions.get(definition.entry_point) : undefined)
    ?? { x: 0, y: 0 };
  const usedIDs = new Set([...definition.nodes.map((node) => node.id), ...virtualNodeIDs]);
  const centerOffset = ((matched.length - 1) * 110) / 2;

  return matched.map((trigger, index) => {
    const canvasID = triggerCanvasNodeID(trigger.id, usedIDs);
    usedIDs.add(canvasID);
    return {
      canvas_id: canvasID,
      trigger,
      position: stored.get(trigger.id) ?? {
        x: anchor.x - 260,
        y: anchor.y + index * 110 - centerOffset,
      },
      valid: triggerConfigurationValid(trigger),
    };
  });
}

export function triggerConfigurationValid(trigger: Trigger): boolean {
  if (typeof trigger.id !== "string" || !trigger.id.trim()) return false;
  if (typeof trigger.target?.graph_id !== "string" || !trigger.target.graph_id.trim()) return false;
  if (trigger.concurrency && trigger.concurrency !== "parallel" && trigger.concurrency !== "skip") return false;
  if (trigger.type === "webhook") {
    if (!trigger.webhook) return false;
    const mappings = trigger.webhook.state_mappings;
    if (mappings !== undefined && !Array.isArray(mappings)) return false;
    return (mappings ?? []).every(
      (mapping) =>
        typeof mapping?.parameter === "string" &&
        Boolean(mapping.parameter.trim()) &&
        typeof mapping?.state_path === "string" &&
        Boolean(mapping.state_path.trim())
    );
  }
  if (trigger.type === "chat") {
    if (!trigger.chat) return false;
    if (trigger.chat.reply_path !== undefined && (typeof trigger.chat.reply_path !== "string" || !trigger.chat.reply_path.trim())) return false;
    return trigger.chat.stream_node_ids === undefined || (
      Array.isArray(trigger.chat.stream_node_ids) && trigger.chat.stream_node_ids.every((nodeID) => typeof nodeID === "string" && Boolean(nodeID.trim()))
    );
  }
  if (trigger.type !== "schedule" || typeof trigger.schedule?.cron !== "string") return false;
  const cron = trigger.schedule.cron.trim();
  if (!cron || cron.split(/\s+/).length !== 5) return false;
  if (trigger.schedule.timezone !== undefined && typeof trigger.schedule.timezone !== "string") return false;
  const timezone = trigger.schedule.timezone?.trim();
  if (!timezone) return true;
  try {
    new Intl.DateTimeFormat("en", { timeZone: timezone }).format();
    return true;
  } catch {
    return false;
  }
}

export function withTriggerCanvasPosition(
  definition: GraphDefinition,
  triggerID: string,
  position: NodePosition,
  validTriggerIDs: string[]
): GraphDefinition {
  const next = withCleanTriggerCanvasPositions(definition, validTriggerIDs);
  const metadata = { ...(next.metadata ?? {}) };
  const web = isRecord(metadata.web) ? { ...metadata.web } : {};
  const triggerNodes = isRecord(web.trigger_nodes) ? { ...web.trigger_nodes } : {};
  triggerNodes[triggerID] = { x: position.x, y: position.y };
  web.trigger_nodes = triggerNodes;
  metadata.web = web;
  return { ...next, metadata };
}

export function withCleanTriggerCanvasPositions(
  definition: GraphDefinition,
  validTriggerIDs: string[]
): GraphDefinition {
  const metadata = { ...(definition.metadata ?? {}) };
  const web = isRecord(metadata.web) ? { ...metadata.web } : {};
  const positions = triggerNodePositions(definition);
  const valid = new Set(validTriggerIDs);
  const triggerNodes: Record<string, NodePosition> = {};
  for (const [triggerID, position] of positions) {
    if (valid.has(triggerID)) triggerNodes[triggerID] = position;
  }
  if (Object.keys(triggerNodes).length > 0) web.trigger_nodes = triggerNodes;
  else delete web.trigger_nodes;
  metadata.web = web;
  return { ...definition, metadata };
}

function triggerNodePositions(definition: GraphDefinition): Map<string, NodePosition> {
  const web = isRecord(definition.metadata?.web) ? definition.metadata.web : undefined;
  const raw = isRecord(web?.trigger_nodes) ? web.trigger_nodes : undefined;
  const result = new Map<string, NodePosition>();
  for (const [triggerID, value] of Object.entries(raw ?? {})) {
    if (!isRecord(value)) continue;
    const x = Number(value.x);
    const y = Number(value.y);
    if (Number.isFinite(x) && Number.isFinite(y)) result.set(triggerID, { x, y });
  }
  return result;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
