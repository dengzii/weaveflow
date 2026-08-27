import type { Node } from "@xyflow/react";
import type { GraphDefinition, GraphNodeSpec, RunStatus, RuntimeEvent, StepRecord, StepStatus, TriggerType } from "../types";
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
  runtimeVisible?: boolean;
  executionCount?: number;
  runTimeMs?: number;
  currentRunTimeMs?: number;
  current?: boolean;
  highlighted?: boolean;
  bindingSummary?: string;
  stateBindingPreview?: NodePreviewItem[];
  missingBindings?: boolean;
  configurationSummary?: string;
  configurationPreview?: NodePreviewItem[];
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

export interface NodePreviewItem {
  name: string;
  value: string;
}

export type RuntimeNodeStatus = "idle" | Exclude<StepStatus, "scheduled">;
export type FlowNodeStatus = RuntimeNodeStatus;

export function triggerNodeRuntimeStatus(runStatus?: RunStatus): RuntimeNodeStatus {
  switch (runStatus) {
    case "running":
      return "running";
    case "paused":
      return "paused";
    case "failed":
      return "failed";
    case "canceled":
      return "canceled";
    case "completed":
      return "succeeded";
    default:
      return "idle";
  }
}

export function virtualNodeRuntimeStatus(
  kind: "start" | "end",
  runStatus?: RunStatus
): RuntimeNodeStatus {
  if (!runStatus) return "idle";
  if (runStatus === "pending") return "idle";
  if (kind === "start") return runStatus === "running" ? "running" : "succeeded";
  return triggerNodeRuntimeStatus(runStatus);
}

export interface RuntimeNodeState {
  status: RuntimeNodeStatus;
  executionCount: number;
  at: number;
  errorMessage?: string;
  stepAttempts?: ReadonlyMap<string, number>;
  stepTimings?: ReadonlyMap<string, RuntimeStepTiming>;
}

export interface RuntimeStepTiming {
  stepID: string;
  attempt?: number;
  startedAt?: number;
  finishedAt?: number;
}

export interface RuntimeTimingUpdate {
  scope: "step" | "attempt";
  startedAt?: number;
  finishedAt?: number;
}

export interface RuntimeDurations {
  totalMs: number;
  currentMs: number;
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
  if (data.runtimeVisible && data.status && data.status !== "idle") parts.push(`execution status ${data.status}`);
  if (data.runtimeVisible && data.executionCount) parts.push(`executions ${data.executionCount}`);
  if (data.runtimeVisible && data.current) parts.push("current node");
  if (data.runtimeVisible && typeof data.runTimeMs === "number") {
    parts.push(`runtime ${formatRuntimeDuration(data.runTimeMs)}`);
  }
  if (data.runtimeVisible && data.status === "running" && typeof data.currentRunTimeMs === "number") {
    parts.push(`current ${formatRuntimeDuration(data.currentRunTimeMs)}`);
  }
  if (data.runtimeVisible && data.errorSummary) parts.push(`error: ${data.errorSummary}`);
  return parts.join(". ");
}

export function runtimeFromSteps(steps: StepRecord[], runID?: string): Map<string, RuntimeNodeState> {
  const runtime = new Map<string, RuntimeNodeState>();
  for (const step of steps) {
    if (!step.node_id) continue;
    if (runID && step.run_id && step.run_id !== runID) continue;
    applyRuntimeStep(
      runtime,
      step.node_id,
      step.step_id,
      normalizeRuntimeStatus(step.status),
      Number.isFinite(step.attempt) ? step.attempt : 0,
      timeRank(step.updated_at || step.finished_at || step.started_at),
      step.error_message,
      stepTimingFromRecord(step)
    );
  }
  return runtime;
}

export function runtimeFromEvents(events: RuntimeEvent[], runID?: string): Map<string, RuntimeNodeState> {
  const runtime = new Map<string, RuntimeNodeState>();
  for (const event of orderedRuntimeEvents(events, runID)) applyRuntimeEvent(runtime, event);
  return runtime;
}

export function runtimeFromExecution(
  steps: StepRecord[],
  events: RuntimeEvent[],
  runID?: string
): Map<string, RuntimeNodeState> {
  const runtime = runtimeFromSteps(steps, runID);
  for (const event of orderedRuntimeEvents(events, runID)) applyRuntimeEvent(runtime, event);
  return runtime;
}

export function applyRuntime(
  runtime: Map<string, RuntimeNodeState>,
  nodeID: string,
  update: RuntimeNodeState
): boolean {
  const current = runtime.get(nodeID);
  if (!current) {
    const next: RuntimeNodeState = {
      ...update,
      stepAttempts: new Map(update.stepAttempts),
    };
    if (update.stepTimings) next.stepTimings = new Map(update.stepTimings);
    runtime.set(nodeID, next);
    return true;
  }

  const stepAttempts = new Map(current.stepAttempts);
  const stepTimings = mergeStepTimings(current.stepTimings, update.stepTimings);
  for (const [stepID, attempt] of update.stepAttempts ?? []) {
    stepAttempts.set(stepID, Math.max(stepAttempts.get(stepID) ?? 0, attempt));
  }
  const executionCount = Math.max(
    totalExecutions(stepAttempts),
    current.executionCount,
    update.executionCount
  );
  const latest = update.at >= current.at ? update : current;
  const next: RuntimeNodeState = {
    status: latest.status,
    executionCount,
    at: latest.at,
    stepAttempts,
  };
  if (stepTimings.size > 0) next.stepTimings = stepTimings;
  const nextErrorMessage = latest.status === "failed" ? latest.errorMessage?.trim() : "";
  if (nextErrorMessage) next.errorMessage = nextErrorMessage;
  if (
    current.status === next.status
    && current.executionCount === next.executionCount
    && current.at === next.at
    && (current.errorMessage ?? "") === (next.errorMessage ?? "")
    && sameStepTimings(current.stepTimings, next.stepTimings)
  ) {
    return false;
  }
  runtime.set(nodeID, next);
  return true;
}

export function applyRuntimeStep(
  runtime: Map<string, RuntimeNodeState>,
  nodeID: string,
  stepID: string,
  status: RuntimeNodeStatus,
  attempt: number,
  at: number,
  errorMessage = "",
  timing?: RuntimeTimingUpdate
): boolean {
  const current = runtime.get(nodeID);
  const stepAttempts = new Map(current?.stepAttempts);
  const normalizedAttempt = Number.isFinite(attempt) ? Math.max(0, Math.trunc(attempt)) : 0;
  const previousAttempt = stepAttempts.get(stepID) ?? 0;
  let stepTimings = new Map(current?.stepTimings);
  if (timing) {
    if (timing.scope === "attempt" && normalizedAttempt > previousAttempt && previousAttempt > 0) {
      const previousKey = attemptTimingKey(stepID, previousAttempt);
      let previousTiming = stepTimings.get(previousKey);
      if (!previousTiming) {
        const stepTiming = stepTimings.get(stepTimingKey(stepID));
        if (stepTiming?.startedAt) {
          previousTiming = { stepID, attempt: previousAttempt, startedAt: stepTiming.startedAt };
        }
      }
      if (previousTiming?.startedAt && !previousTiming.finishedAt && at >= previousTiming.startedAt) {
        stepTimings.set(previousKey, { ...previousTiming, finishedAt: at });
      }
    }
    const timingAttempt = normalizedAttempt || previousAttempt || 1;
    const timingKey = timing.scope === "attempt"
      ? attemptTimingKey(stepID, timingAttempt)
      : stepTimingKey(stepID);
    const nextTiming: RuntimeStepTiming = {
      stepID,
      attempt: timing.scope === "attempt" ? timingAttempt : undefined,
      startedAt: timing.startedAt,
      finishedAt: timing.finishedAt,
    };
    stepTimings = mergeStepTimings(stepTimings, new Map([[timingKey, nextTiming]]));
  }
  if (normalizedAttempt > previousAttempt) stepAttempts.set(stepID, normalizedAttempt);
  const trackedExecutions = totalExecutions(current?.stepAttempts ?? new Map());
  const untrackedExecutions = Math.max(0, (current?.executionCount ?? 0) - trackedExecutions);
  const executionCount = untrackedExecutions + totalExecutions(stepAttempts);
  const latest = !current || at >= current.at;
  const next: RuntimeNodeState = {
    status: latest ? status : current.status,
    executionCount,
    at: latest ? at : current.at,
    stepAttempts,
  };
  if (stepTimings.size > 0) next.stepTimings = stepTimings;
  const nextErrorMessage = latest
    ? status === "failed" ? errorMessage.trim() : ""
    : current.errorMessage ?? "";
  if (nextErrorMessage) next.errorMessage = nextErrorMessage;
  if (
    current
    && current.status === next.status
    && current.executionCount === next.executionCount
    && current.at === next.at
    && (current.errorMessage ?? "") === (next.errorMessage ?? "")
    && sameStepTimings(current.stepTimings, next.stepTimings)
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
    if (
      (node.data.status || "idle") === "idle"
      && !node.data.executionCount
      && !node.data.runTimeMs
      && !node.data.currentRunTimeMs
      && !node.data.current
      && !node.data.errorSummary
    ) return node;
    changed = true;
    return {
      ...node,
      data: {
        ...node.data,
        status: "idle",
        executionCount: 0,
        runTimeMs: undefined,
        currentRunTimeMs: undefined,
        current: undefined,
        errorSummary: undefined,
      },
      ariaLabel: flowNodeAriaLabel({
        ...node.data,
        status: "idle",
        executionCount: 0,
        runTimeMs: undefined,
        currentRunTimeMs: undefined,
        current: undefined,
        errorSummary: undefined,
      }),
    };
  });
  return changed ? next : nodes;
}

export function runtimeDurations(runtime?: RuntimeNodeState, now = Date.now()): RuntimeDurations {
  if (!runtime) return { totalMs: 0, currentMs: 0 };
  const timings = runtime.stepTimings;
  if (!timings || timings.size === 0) {
    if (runtime.status !== "running" || runtime.at <= 0) return { totalMs: 0, currentMs: 0 };
    const currentMs = Math.max(0, now - runtime.at);
    return { totalMs: currentMs, currentMs };
  }

  const attemptNumbersByStep = new Map<string, Set<number>>();
  const activeAttemptStepIDs = new Set<string>();
  for (const timing of timings.values()) {
    if (!timing.attempt || !timing.startedAt || timing.startedAt <= 0) continue;
    const attempts = attemptNumbersByStep.get(timing.stepID) ?? new Set<number>();
    attempts.add(timing.attempt);
    attemptNumbersByStep.set(timing.stepID, attempts);
    if (!timing.finishedAt) activeAttemptStepIDs.add(timing.stepID);
  }
  const completeAttemptStepIDs = new Set<string>();
  for (const [stepID, attempts] of attemptNumbersByStep) {
    const expectedAttempts = runtime.stepAttempts?.get(stepID) ?? Math.max(...attempts);
    if (expectedAttempts > 0 && attempts.size >= expectedAttempts) {
      let complete = true;
      for (let attempt = 1; attempt <= expectedAttempts; attempt++) {
        if (!attempts.has(attempt)) {
          complete = false;
          break;
        }
      }
      if (complete) completeAttemptStepIDs.add(stepID);
    }
  }

  let totalMs = 0;
  let currentMs = 0;
  for (const timing of timings.values()) {
    if (!timing.startedAt || timing.startedAt <= 0) continue;
    const end = timing.finishedAt && timing.finishedAt >= timing.startedAt
      ? timing.finishedAt
      : runtime.status === "running"
        ? now
        : runtime.at;
    const durationMs = Math.max(0, end - timing.startedAt);
    const useAttemptTotal = timing.attempt && completeAttemptStepIDs.has(timing.stepID);
    const useStepTotal = !timing.attempt && !completeAttemptStepIDs.has(timing.stepID);
    if (useAttemptTotal || useStepTotal) totalMs += durationMs;
    if (!timing.finishedAt && runtime.status === "running") {
      const useAttemptCurrent = Boolean(timing.attempt);
      const useStepCurrent = !timing.attempt && !activeAttemptStepIDs.has(timing.stepID);
      if (useAttemptCurrent || useStepCurrent) currentMs = Math.max(currentMs, durationMs);
    }
  }
  return { totalMs, currentMs };
}

export function formatRuntimeDuration(durationMs: number): string {
  if (!Number.isFinite(durationMs) || durationMs <= 0) return "0ms";
  if (durationMs < 1_000) return `${Math.round(durationMs)}ms`;
  const seconds = durationMs / 1_000;
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = Math.floor(seconds % 60);
  if (minutes < 60) return `${minutes}m${remainingSeconds}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h${minutes % 60}m`;
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

export function eventAttempt(type: string, payload: unknown): number {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    return type === "nodes.started" ? 1 : 0;
  }
  const record = payload as Record<string, unknown>;
  const value = type === "nodes.retry" ? record.next_attempt ?? record.attempt : record.attempt;
  if (typeof value === "number" && Number.isFinite(value)) return value;
  return type === "nodes.started" ? 1 : 0;
}

export function eventAttemptStartedAt(type: string, payload: unknown, eventAt: number): number {
  if (type !== "nodes.retry" || !payload || typeof payload !== "object" || Array.isArray(payload)) {
    return eventAt;
  }
  const delay = (payload as Record<string, unknown>).delay;
  return eventAt + (typeof delay === "string" ? parseGoDurationMilliseconds(delay) : 0);
}

export function applyRuntimeEvent(runtime: Map<string, RuntimeNodeState>, event: RuntimeEvent): boolean {
  if (!event.node_id) return false;
  const status = runtimeStatusFromEvent(event.type);
  if (!status) return false;
  const eventAt = timeRank(event.timestamp);
  return applyRuntimeStep(
    runtime,
    event.node_id,
    event.step_id || `${event.node_id}:current`,
    status,
    eventAttempt(event.type, event.payload),
    eventAt,
    eventErrorMessage(event.payload),
    event.type === "nodes.started" || event.type === "nodes.retry"
      ? { scope: "attempt", startedAt: eventAttemptStartedAt(event.type, event.payload, eventAt) }
      : { scope: "attempt", finishedAt: eventAt }
  );
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
  const executionCount = runtime.executionCount || 0;
  const durations = runtimeDurations(runtime);
  const runTimeMs = durations.totalMs;
  const currentRunTimeMs = runtime.status === "running" ? durations.currentMs : undefined;
  const current = runtime.status === "running";
  const errorSummary = runtime.status === "failed" ? runtime.errorMessage || undefined : undefined;
  if (
    node.data.status === runtime.status
    && node.data.executionCount === executionCount
    && node.data.runTimeMs === runTimeMs
    && node.data.currentRunTimeMs === currentRunTimeMs
    && node.data.current === current
    && node.data.errorSummary === errorSummary
  ) return node;
  const data = {
    ...node.data,
    status: runtime.status,
    executionCount,
    runTimeMs,
    currentRunTimeMs,
    current,
    errorSummary,
  };
  return {
    ...node,
    data,
    ariaLabel: flowNodeAriaLabel(data),
  };
}

function totalExecutions(stepAttempts: ReadonlyMap<string, number>): number {
  let total = 0;
  for (const attempt of stepAttempts.values()) total += attempt;
  return total;
}

function stepTimingFromRecord(step: StepRecord): RuntimeTimingUpdate | undefined {
  const startedAt = timeRank(step.started_at);
  const finishedAt = step.status === "running"
    ? undefined
    : timeRank(step.finished_at || step.updated_at);
  if (startedAt <= 0 && (!finishedAt || finishedAt <= 0)) return undefined;
  return {
    scope: "step",
    startedAt: startedAt > 0 ? startedAt : undefined,
    finishedAt: finishedAt > 0 ? finishedAt : undefined,
  };
}

function mergeStepTimings(
  current?: ReadonlyMap<string, RuntimeStepTiming>,
  update?: ReadonlyMap<string, RuntimeStepTiming>
): Map<string, RuntimeStepTiming> {
  const merged = new Map(current);
  for (const [stepID, timing] of update ?? []) {
    const previous = merged.get(stepID);
    const startedAt = previous?.startedAt || timing.startedAt;
    const finishedAt = previous?.finishedAt && timing.finishedAt
      ? Math.max(previous.finishedAt, timing.finishedAt)
      : previous?.finishedAt || timing.finishedAt;
    if (startedAt || finishedAt) {
      merged.set(stepID, {
        stepID: previous?.stepID || timing.stepID,
        attempt: previous?.attempt || timing.attempt,
        startedAt,
        finishedAt,
      });
    }
  }
  return merged;
}

function sameStepTimings(
  left?: ReadonlyMap<string, RuntimeStepTiming>,
  right?: ReadonlyMap<string, RuntimeStepTiming>
): boolean {
  if ((left?.size ?? 0) !== (right?.size ?? 0)) return false;
  for (const [stepID, timing] of left ?? []) {
    const other = right?.get(stepID);
    if (
      !other
      || timing.stepID !== other.stepID
      || timing.attempt !== other.attempt
      || timing.startedAt !== other.startedAt
      || timing.finishedAt !== other.finishedAt
    ) return false;
  }
  return true;
}

function stepTimingKey(stepID: string): string {
  return `step:${stepID}`;
}

function attemptTimingKey(stepID: string, attempt: number): string {
  return `attempt:${attempt}:${stepID}`;
}

function parseGoDurationMilliseconds(value: string): number {
  const normalized = value.trim();
  if (!normalized) return 0;
  const units: Record<string, number> = {
    h: 3_600_000,
    m: 60_000,
    s: 1_000,
    ms: 1,
    us: 0.001,
    "µs": 0.001,
    ns: 0.000_001,
  };
  let matched = "";
  let durationMs = 0;
  for (const token of normalized.matchAll(/(\d+(?:\.\d+)?)(ns|µs|us|ms|s|m|h)/g)) {
    matched += token[0];
    durationMs += Number(token[1]) * units[token[2]];
  }
  return matched === normalized && Number.isFinite(durationMs) ? Math.max(0, durationMs) : 0;
}

function orderedRuntimeEvents(events: RuntimeEvent[], runID?: string): RuntimeEvent[] {
  return events
    .map((event, index) => ({ event, index, at: timeRank(event.timestamp) }))
    .filter(({ event }) => !runID || !event.run_id || event.run_id === runID)
    .sort((left, right) => left.at - right.at || right.index - left.index)
    .map(({ event }) => event);
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
