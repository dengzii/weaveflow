import type {
  InitialStateRequirement,
  RunInterrupt,
  RunRecord,
  RunStatus,
  RuntimeEvent,
  TriggerInvocation,
  TriggerType,
} from "../../types";
import { hasFilledInitialStatePath } from "./graph-workspace/runInputModel";

export interface GraphIdentity {
  id: string;
  version: string;
}

export type RunControlMode = "run" | "active" | "resume";

export function runStatusFromEvent(eventType: string): RunStatus | "" {
  switch (eventType) {
    case "run.created":
      return "pending";
    case "run.started":
    case "run.resumed":
      return "running";
    case "run.paused":
      return "paused";
    case "run.canceled":
      return "canceled";
    case "run.finished":
      return "completed";
    case "run.failed":
      return "failed";
    default:
      return "";
  }
}

export function runControlModeFromRun(
  run: RunRecord | null,
  interrupt?: RunInterrupt | null
): RunControlMode {
  if (!run) return "run";
  if (isResumableRunStatus(run.status)) return hasResumeCheckpoint(run, interrupt) ? "resume" : "run";
  return isActiveRunStatus(run.status) ? "active" : "run";
}

export function canResumeRun(run: RunRecord | null, interrupt?: RunInterrupt | null): boolean {
  return Boolean(run && isResumableRunStatus(run.status) && hasResumeCheckpoint(run, interrupt));
}

export function isActiveRunStatus(status: RunStatus): boolean {
  return status === "pending" || status === "running";
}

export function matchesGraphIdentity(run: RunRecord, identity: GraphIdentity): boolean {
  return run.graph_id === identity.id && run.graph_version === identity.version;
}

export type RunListEventAction = "ignore" | "refresh" | "update";

export function runListEventAction(runs: RunRecord[], event: RuntimeEvent): RunListEventAction {
  if (!event.run_id) return "ignore";
  if (runs.some((run) => run.run_id === event.run_id)) return "update";
  return runStatusFromEvent(event.type) ? "refresh" : "ignore";
}

export function mergeRefreshedRuns(
  current: RunRecord[],
  refreshed: RunRecord[],
  started: RunRecord[] = current
): RunRecord[] {
  const currentByID = new Map(current.map((run) => [run.run_id, run]));
  const startedIDs = new Set(started.map((run) => run.run_id));
  const refreshedIDs = new Set(refreshed.map((run) => run.run_id));
  const merged = refreshed.flatMap((run) => {
    const existing = currentByID.get(run.run_id);
    if (!existing && startedIDs.has(run.run_id)) return [];
    return [existing && Date.parse(existing.updated_at) > Date.parse(run.updated_at) ? existing : run];
  });
  return merged.concat(
    current.filter((run) => !startedIDs.has(run.run_id) && !refreshedIDs.has(run.run_id))
  );
}

export function upsertInspectedRun(current: RunRecord[], inspected: RunRecord): RunRecord[] {
  const existingIndex = current.findIndex((run) => run.run_id === inspected.run_id);
  if (existingIndex < 0) return [...current, inspected];

  const existing = current[existingIndex];
  if (isEarlierTimestamp(inspected.updated_at, existing.updated_at)) return current;
  return current.map((run, index) => (index === existingIndex ? inspected : run));
}

export function upsertRunFromEvent(
  runs: RunRecord[],
  event: RuntimeEvent,
  nextStatus: RunStatus | "",
  graphIdentity: GraphIdentity
): RunRecord[] {
  if (!event.run_id) return runs;
  let found = false;
  let changed = false;
  const checkpointID = stringPayloadField(event.payload, "checkpoint_id");
  const updated = runs.map((run) => {
    if (run.run_id !== event.run_id) return run;
    found = true;
    if (!nextStatus && !checkpointID) return run;
    if (isEarlierTimestamp(event.timestamp, run.updated_at)) return run;
    const status = nextStatus || run.status;
    const next = {
      ...run,
      status,
      updated_at: event.timestamp,
      finished_at: isTerminalRunStatus(status) ? event.timestamp : run.finished_at,
      last_checkpoint_id: checkpointID || run.last_checkpoint_id,
    };
    if (
      next.status === run.status &&
      next.updated_at === run.updated_at &&
      next.finished_at === run.finished_at &&
      next.last_checkpoint_id === run.last_checkpoint_id
    ) {
      return run;
    }
    changed = true;
    return next;
  });
  if (found) return changed ? updated : runs;

  const status = nextStatus || "running";
  return [
    ...updated,
    {
      run_id: event.run_id,
      graph_id: graphIdentity.id || "graph",
      graph_version: graphIdentity.version || "1.0",
      status,
      entry_node_id: stringPayloadField(event.payload, "entry_node_id"),
      last_checkpoint_id: stringPayloadField(event.payload, "checkpoint_id") || undefined,
      started_at: event.timestamp,
      updated_at: event.timestamp,
      finished_at: isTerminalRunStatus(status) ? event.timestamp : undefined,
    },
  ];
}

export function mergeLiveRuntimeEvents(
  current: RuntimeEvent[],
  incoming: RuntimeEvent[],
  retainRunID: string
): RuntimeEvent[] {
  if (incoming.length === 0) return current;

  const chronological = [...current].reverse().concat(incoming);
  const retained = retainRunID
    ? chronological.filter((event) => !event.run_id || event.run_id === retainRunID)
    : chronological;
  const streamingEvents = new Map<string, RuntimeEvent>();

  for (const event of retained) {
    const key = streamingEventKey(event);
    if (!key) continue;
    const previous = streamingEvents.get(key);
    streamingEvents.set(key, previous ? mergeStreamingEvent(previous, event) : event);
  }

  const emittedStreamingKeys = new Set<string>();
  const newestFirst: RuntimeEvent[] = [];
  for (let index = retained.length - 1; index >= 0; index -= 1) {
    const event = retained[index];
    const key = streamingEventKey(event);
    if (!key) {
      newestFirst.push(event);
      continue;
    }
    if (emittedStreamingKeys.has(key)) continue;
    emittedStreamingKeys.add(key);
    newestFirst.push(streamingEvents.get(key) ?? event);
  }
  return newestFirst.slice(0, MAX_LIVE_RUNTIME_EVENTS);
}

export const MAX_LIVE_RUNTIME_EVENTS = 5000;

export function mergeStoredRuntimeEvents(
  current: RuntimeEvent[],
  older: RuntimeEvent[]
): RuntimeEvent[] {
  const seen = new Set<string>();
  return [...current, ...older].filter((event) => {
    const key = runtimeEventIdentity(event);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export function reconcileRunEvents(
  liveEvents: RuntimeEvent[],
  storedEvents: RuntimeEvent[],
  selectedRunID: string
): RuntimeEvent[] {
  const seen = new Set<string>();
  return [...liveEvents, ...storedEvents].filter((event) => {
    if (selectedRunID && event.run_id !== selectedRunID) return false;
    const key = runtimeEventIdentity(event);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function runtimeEventIdentity(event: RuntimeEvent): string {
  return event.id || `${event.run_id}:${event.type}:${event.timestamp}:${event.node_id ?? ""}:${event.step_id ?? ""}`;
}

export function runTriggerTypesFromInvocations(
  invocations: TriggerInvocation[],
  graphID: string
): Partial<Record<string, TriggerType>> {
  const triggerTypes: Partial<Record<string, TriggerType>> = {};
  for (const invocation of invocations) {
    const runID = invocation.run?.run_id;
    if (invocation.target.graph_id === graphID && runID) {
      triggerTypes[runID] = invocation.trigger_type;
    }
  }
  return triggerTypes;
}

export function markRunResuming(runs: RunRecord[], runID: string): RunRecord[] {
  return runs.map((run) =>
    run.run_id === runID
      ? { ...run, status: "running", pause_requested: false }
      : run
  );
}

export function selectedRunIDAfterDeletion(
  runs: RunRecord[],
  deletedRunID: string,
  selectedRunID: string
): string {
  if (selectedRunID !== deletedRunID && runs.some((run) => run.run_id === selectedRunID)) {
    return selectedRunID;
  }
  return runs.filter((run) => run.run_id !== deletedRunID).at(-1)?.run_id || "";
}

export function missingInitialStateRequirements(
  initialState: unknown,
  requirements: InitialStateRequirement[]
): string[] {
  return requirements
    .filter((requirement) => !hasFilledInitialStatePath(initialState, requirement.path, requirement.type))
    .map((requirement) => requirement.path);
}

export function isTerminalRunStatus(status: RunStatus): boolean {
  return status === "completed" || status === "failed" || status === "canceled";
}

function hasResumeCheckpoint(run: RunRecord, interrupt?: RunInterrupt | null): boolean {
  return Boolean(run.last_checkpoint_id || (interrupt?.run_id === run.run_id && interrupt.checkpoint_id));
}

function isResumableRunStatus(status: RunStatus): boolean {
  return status === "paused";
}

function stringPayloadField(payload: unknown, field: string): string {
  const value = payloadRecord(payload)?.[field];
  return typeof value === "string" ? value : "";
}

function streamingEventKey(event: RuntimeEvent): string {
  if (event.type !== "llm.content_chunk" && event.type !== "llm.reasoning_chunk") return "";
  const callID = stringPayloadField(event.payload, "call_id");
  const text = stringPayloadField(event.payload, "text");
  if (!callID || !text) return "";
  return JSON.stringify([event.run_id, event.step_id ?? "", event.node_id ?? "", event.type, callID]);
}

function mergeStreamingEvent(previous: RuntimeEvent, event: RuntimeEvent): RuntimeEvent {
  const payload = payloadRecord(event.payload) ?? {};
  return {
    ...event,
    id: previous.id || event.id,
    payload: {
      ...payload,
      text: stringPayloadField(previous.payload, "text") + stringPayloadField(event.payload, "text"),
    },
  };
}

function payloadRecord(payload: unknown): Record<string, unknown> | null {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  return payload as Record<string, unknown>;
}

function isEarlierTimestamp(candidate: string, current: string): boolean {
  const candidateTime = Date.parse(candidate);
  const currentTime = Date.parse(current);
  return !Number.isNaN(candidateTime) && !Number.isNaN(currentTime) && candidateTime < currentTime;
}
