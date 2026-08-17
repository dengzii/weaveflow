import type { Node } from "@xyflow/react";
import type { GraphDefinition, GraphNodeSpec, StepRecord, StepStatus, TriggerType } from "../types";
import {
  END_NODE_REF,
  START_NODE_REF,
  graphNodePositions,
  type NodePosition,
} from "../lib/graphEditor";
import { analyzeVirtualGraphLoop, type VirtualGraphLoop } from "../lib/loopPresentation";

export interface FlowNodeData extends Record<string, unknown> {
  label: string;
  type: string;
  typeLabel?: string;
  status: FlowNodeStatus;
  editable: boolean;
  attempt?: number;
  highlighted?: boolean;
  bindingSummary?: string;
  missingBindings?: boolean;
  configurationSummary?: string;
  configurationErrors?: readonly string[];
  errorSummary?: string;
  virtualKind?: "start" | "end" | "loop" | "trigger";
  triggerID?: string;
  triggerType?: TriggerType;
  triggerEnabled?: boolean;
  triggerValid?: boolean;
  width?: number;
  height?: number;
}

export type RuntimeNodeStatus = "idle" | Exclude<StepStatus, "scheduled">;
export type FlowNodeStatus = RuntimeNodeStatus | "enabled" | "disabled";

export interface RuntimeNodeState {
  status: RuntimeNodeStatus;
  attempt: number;
  at: number;
  errorMessage?: string;
}

export interface VirtualLoopLayout {
  loop: VirtualGraphLoop;
  nodeIDs: string[];
  nodeIDSet: Set<string>;
  position: NodePosition;
  width: number;
  height: number;
}

export const graphNodeWidth = 176;
export const graphNodeHeight = 68;
export const minGraphLoopWidth = 250;
export const minGraphLoopHeight = 150;
export const triggerTargetHandleID = "trigger-input";
export const graphNodeDimensions = { width: graphNodeWidth, height: graphNodeHeight };

const loopPaddingX = 62;
const loopPaddingTop = 54;
const loopPaddingBottom = 20;

export function flowNodeAriaLabel(data: FlowNodeData): string {
  const parts = [data.label];
  const typeLabel = data.typeLabel || data.type;
  if (typeLabel && typeLabel !== data.label) parts.push(`type ${typeLabel}`);
  if (data.configurationErrors?.length) {
    parts.push(`configuration error: ${data.configurationErrors[0]}`);
  } else {
    if (data.configurationSummary) parts.push(data.configurationSummary);
    if (data.bindingSummary) parts.push(data.bindingSummary);
  }
  if (data.status && data.status !== "idle") parts.push(`execution status ${data.status}`);
  if (data.attempt) parts.push(`attempt ${data.attempt}`);
  if (data.errorSummary) parts.push(`error: ${data.errorSummary}`);
  return parts.join(". ");
}

export function runtimeFromSteps(steps: StepRecord[], runID?: string): Map<string, RuntimeNodeState> {
  const runtime = new Map<string, RuntimeNodeState>();
  for (const step of steps) {
    if (!step.node_id) continue;
    if (runID && step.run_id && step.run_id !== runID) continue;
    applyRuntime(
      runtime,
      step.node_id,
      normalizeRuntimeStatus(step.status),
      Number.isFinite(step.attempt) ? step.attempt : 0,
      timeRank(step.updated_at || step.finished_at || step.started_at),
      step.error_message
    );
  }
  return runtime;
}

export function applyRuntime(
  runtime: Map<string, RuntimeNodeState>,
  nodeID: string,
  status: RuntimeNodeStatus,
  attempt: number,
  at: number,
  errorMessage = ""
): boolean {
  const current = runtime.get(nodeID);
  const nextAttempt = Math.max(current?.attempt ?? 0, attempt);
  if (current && current.at > at) {
    if (nextAttempt === current.attempt) return false;
    runtime.set(nodeID, { ...current, attempt: nextAttempt });
    return true;
  }

  const next: RuntimeNodeState = {
    status,
    attempt: nextAttempt,
    at,
  };
  const nextErrorMessage = status === "failed" ? errorMessage.trim() : "";
  if (nextErrorMessage) next.errorMessage = nextErrorMessage;
  if (
    current
    && current.status === next.status
    && current.attempt === next.attempt
    && current.at === next.at
    && (current.errorMessage ?? "") === (next.errorMessage ?? "")
  ) {
    return false;
  }
  runtime.set(nodeID, next);
  return true;
}

export function applyRuntimeSnapshot(
  nodes: Node<FlowNodeData>[],
  runtime: Map<string, RuntimeNodeState>
): Node<FlowNodeData>[] {
  let changed = false;
  const next = nodes.map((node) => {
    if (node.data.virtualKind) return node;
    const update = runtime.get(node.id);
    if (!update) return node;
    const updated = updateRuntimeNodeData(node, update);
    if (updated !== node) changed = true;
    return updated;
  });
  return changed ? next : nodes;
}

export function updateRuntimeNode(
  nodes: Node<FlowNodeData>[],
  nodeID: string,
  runtime?: RuntimeNodeState
): Node<FlowNodeData>[] {
  if (!runtime) return nodes;
  let changed = false;
  const next = nodes.map((node) => {
    if (node.id !== nodeID || node.data.virtualKind) return node;
    const updated = updateRuntimeNodeData(node, runtime);
    if (updated !== node) changed = true;
    return updated;
  });
  return changed ? next : nodes;
}

export function resetRuntimeNodes(nodes: Node<FlowNodeData>[]): Node<FlowNodeData>[] {
  let changed = false;
  const next = nodes.map((node) => {
    if (node.data.virtualKind) return node;
    if ((node.data.status || "idle") === "idle" && !node.data.attempt && !node.data.errorSummary) return node;
    changed = true;
    return {
      ...node,
      data: {
        ...node.data,
        status: "idle",
        attempt: 0,
        errorSummary: undefined,
      },
      ariaLabel: flowNodeAriaLabel({
        ...node.data,
        status: "idle",
        attempt: 0,
        errorSummary: undefined,
      }),
    };
  });
  return changed ? next : nodes;
}

export function runtimeStatusFromEvent(type: string): RuntimeNodeStatus | "" {
  switch (type) {
    case "nodes.started":
    case "nodes.retry":
      return "running";
    case "nodes.finished":
      return "succeeded";
    case "nodes.failed":
      return "failed";
    case "nodes.canceled":
      return "canceled";
    default:
      return "";
  }
}

export function eventAttempt(payload: unknown): number {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return 0;
  const value = (payload as Record<string, unknown>).attempt;
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export function eventErrorMessage(payload: unknown): string {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return "";
  const record = payload as Record<string, unknown>;
  const value = record.error_message ?? record.error;
  return typeof value === "string" ? value.trim() : "";
}

export function timeRank(value?: string): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

export function virtualLoopLayouts(
  definition: GraphDefinition,
  loops: VirtualGraphLoop[],
  positions: Map<string, NodePosition>
): VirtualLoopLayout[] {
  const nodeIDs = new Set(definition.nodes.map((node) => node.id));
  const savedPositions = graphNodePositions(definition);
  return loops.map((loop) => {
    const analysis = analyzeVirtualGraphLoop(definition, loop);
    const validNodeIDs = analysis.nodeIds.filter((nodeID) => nodeIDs.has(nodeID));
    const bounds = loopBounds(loop.id, validNodeIDs, positions, savedPositions);
    return {
      loop,
      nodeIDs: validNodeIDs,
      nodeIDSet: new Set(validNodeIDs),
      ...bounds,
    };
  });
}

export function layoutNodes(definition: GraphDefinition, virtualNodeIDs: Set<string>): Map<string, NodePosition> {
  const levels = new Map<string, number>();
  const outgoing = new Map<string, string[]>();
  const entry = definition.entry_point;
  const finish = definition.finish_point;
  const layoutEntry = entry || definition.nodes[0]?.id;
  const startVirtualNodeIDs = [...virtualNodeIDs].filter(isVirtualStartNodeID);
  const endVirtualNodeIDs = [...virtualNodeIDs].filter(isVirtualEndNodeID);
  const endVirtualNodeIDSet = new Set(endVirtualNodeIDs);
  for (const edge of definition.edges ?? []) {
    outgoing.set(edge.from, [...(outgoing.get(edge.from) ?? []), edge.to]);
  }
  if (entry) {
    for (const startID of startVirtualNodeIDs) outgoing.set(startID, [entry]);
  }
  if (finish) outgoing.set(finish, [...(outgoing.get(finish) ?? []), ...endVirtualNodeIDs]);

  const queue: Array<{ id: string; level: number }> = [];
  if (startVirtualNodeIDs.length > 0) {
    for (const startID of startVirtualNodeIDs) queue.push({ id: startID, level: -1 });
  } else if (layoutEntry) {
    queue.push({ id: layoutEntry, level: 0 });
  }

  while (queue.length > 0) {
    const item = queue.shift()!;
    const current = levels.get(item.id);
    if (current !== undefined && current <= item.level) continue;
    levels.set(item.id, item.level);
    for (const next of outgoing.get(item.id) ?? []) queue.push({ id: next, level: item.level + 1 });
  }

  for (const node of definition.nodes) {
    if (!levels.has(node.id)) levels.set(node.id, 0);
  }
  for (const startID of startVirtualNodeIDs) levels.set(startID, Math.min(levels.get(startID) ?? -1, -1));
  const maxLevel = Math.max(
    0,
    ...[...levels.entries()].filter(([id]) => !endVirtualNodeIDSet.has(id)).map(([, level]) => level)
  );
  for (const endID of endVirtualNodeIDs) {
    levels.set(endID, Math.max(levels.get(endID) ?? maxLevel + 1, maxLevel + 1));
  }

  const buckets = new Map<number, string[]>();
  for (const id of [...startVirtualNodeIDs, ...definition.nodes.map((node) => node.id), ...endVirtualNodeIDs]) {
    const level = levels.get(id) ?? 0;
    buckets.set(level, [...(buckets.get(level) ?? []), id]);
  }

  const positions = new Map<string, NodePosition>();
  const savedPositions = graphNodePositions(definition);
  for (const [level, ids] of buckets) {
    ids.forEach((id, index) => {
      positions.set(id, savedPositions.get(id) ?? { x: level * 260, y: index * 130 });
    });
  }
  return positions;
}

export function virtualNodeSpec(nodeID: string): GraphNodeSpec {
  const kind = virtualNodeKind(nodeID);
  return {
    id: nodeID,
    name: virtualNodeLabel(nodeID),
    type: kind ?? "node",
    config: {},
  };
}

export function virtualNodeKind(nodeID: string): "start" | "end" | undefined {
  if (isVirtualStartNodeID(nodeID)) return "start";
  if (isVirtualEndNodeID(nodeID)) return "end";
  return undefined;
}

export function isVirtualStartNodeID(nodeID: string): boolean {
  return nodeID === START_NODE_REF || nodeID.startsWith(`${START_NODE_REF}:`);
}

export function isVirtualEndNodeID(nodeID: string): boolean {
  return nodeID === END_NODE_REF || nodeID.startsWith(`${END_NODE_REF}:`);
}

function updateRuntimeNodeData(node: Node<FlowNodeData>, runtime: RuntimeNodeState): Node<FlowNodeData> {
  const attempt = runtime.attempt || 0;
  const errorSummary = runtime.status === "failed" ? runtime.errorMessage || undefined : undefined;
  if (
    node.data.status === runtime.status
    && node.data.attempt === attempt
    && node.data.errorSummary === errorSummary
  ) return node;
  const data = { ...node.data, status: runtime.status, attempt, errorSummary };
  return {
    ...node,
    data,
    ariaLabel: flowNodeAriaLabel(data),
  };
}

function normalizeRuntimeStatus(status: StepStatus): RuntimeNodeStatus {
  switch (status) {
    case "scheduled":
      return "idle";
    case "running":
      return "running";
    case "succeeded":
      return "succeeded";
    case "failed":
      return "failed";
    case "paused":
      return "paused";
    case "canceled":
      return "canceled";
    default:
      return "idle";
  }
}

function loopBounds(
  loopID: string,
  nodeIDs: string[],
  positions: Map<string, NodePosition>,
  savedPositions: Map<string, NodePosition>
) {
  if (nodeIDs.length === 0) {
    return {
      position: savedPositions.get(loopID) ?? { x: 0, y: 0 },
      width: minGraphLoopWidth,
      height: minGraphLoopHeight,
    };
  }

  const bounds = nodeIDs.reduce(
    (current, nodeID) => {
      const position = positions.get(nodeID) ?? { x: 0, y: 0 };
      return {
        minX: Math.min(current.minX, position.x),
        minY: Math.min(current.minY, position.y),
        maxX: Math.max(current.maxX, position.x + graphNodeWidth),
        maxY: Math.max(current.maxY, position.y + graphNodeHeight),
      };
    },
    { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity }
  );

  if (!Number.isFinite(bounds.minX) || !Number.isFinite(bounds.minY)) {
    return {
      position: savedPositions.get(loopID) ?? { x: 0, y: 0 },
      width: minGraphLoopWidth,
      height: minGraphLoopHeight,
    };
  }

  return {
    position: { x: bounds.minX - loopPaddingX, y: bounds.minY - loopPaddingTop },
    width: Math.max(minGraphLoopWidth, bounds.maxX - bounds.minX + loopPaddingX * 2),
    height: Math.max(minGraphLoopHeight, bounds.maxY - bounds.minY + loopPaddingTop + loopPaddingBottom),
  };
}

function virtualNodeLabel(nodeID: string): string {
  const kind = virtualNodeKind(nodeID);
  const index = virtualNodeIndex(nodeID);
  const label = kind === "start" ? "Start" : "End";
  return index > 1 ? `${label} ${index}` : label;
}

function virtualNodeIndex(nodeID: string): number {
  const kind = virtualNodeKind(nodeID);
  const base = kind === "start" ? START_NODE_REF : kind === "end" ? END_NODE_REF : "";
  const prefix = `${base}:`;
  if (!base || nodeID === base || !nodeID.startsWith(prefix)) return 1;
  const index = Number(nodeID.slice(prefix.length));
  return Number.isInteger(index) && index > 1 ? index : 1;
}
