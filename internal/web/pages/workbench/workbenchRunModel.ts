import type {
  InitialStateRequirement,
  RunInterrupt,
  RunRecord,
  RuntimeEvent,
  TriggerRecord,
  TriggerType,
} from "../../types";
import { hasFilledInitialStatePath } from "./graph-workspace/runInputModel";

export interface GraphIdentity {
  id: string;
  version: string;
}

export type RunControlMode = "run" | "active" | "resume";

export function runStatusFromEvent(eventType: string): string {
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
      return "finished";
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

export function isActiveRunStatus(status: string): boolean {
  return status === "pending" || status === "running";
}

export function matchesGraphIdentity(run: RunRecord, identity: GraphIdentity): boolean {
  return run.graph_id === identity.id && run.graph_version === identity.version;
}

export function upsertRunFromEvent(
  runs: RunRecord[],
  event: RuntimeEvent,
  nextStatus: string,
  graphIdentity: GraphIdentity
): RunRecord[] {
  if (!event.run_id) return runs;
  let found = false;
  const updated = runs.map((run) => {
    if (run.run_id !== event.run_id) return run;
    found = true;
    const status = nextStatus || run.status;
    return {
      ...run,
      status,
      updated_at: event.timestamp,
      finished_at: isTerminalRunStatus(status) ? event.timestamp : run.finished_at,
      last_checkpoint_id: stringPayloadField(event.payload, "checkpoint_id") || run.last_checkpoint_id,
    };
  });
  if (found) return updated;

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

export function reconcileRunEvents(
  liveEvents: RuntimeEvent[],
  storedEvents: RuntimeEvent[],
  selectedRunID: string
): RuntimeEvent[] {
  const seen = new Set<string>();
  return [...liveEvents, ...storedEvents].filter((event) => {
    if (selectedRunID && event.run_id !== selectedRunID) return false;
    const key = event.id || `${event.run_id}:${event.type}:${event.timestamp}:${event.node_id ?? ""}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export function runTriggerTypesFromRecords(
  records: TriggerRecord[],
  graphID: string
): Partial<Record<string, TriggerType>> {
  const triggerTypes: Partial<Record<string, TriggerType>> = {};
  for (const record of records) {
    const runID = record.run?.run_id;
    if (record.target.graph_id === graphID && runID) {
      triggerTypes[runID] = record.trigger_type;
    }
  }
  return triggerTypes;
}

export function markRunResuming(runs: RunRecord[], runID: string, updatedAt: string): RunRecord[] {
  return runs.map((run) =>
    run.run_id === runID
      ? { ...run, status: "running", pause_requested: false, updated_at: updatedAt }
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

export function isTerminalRunStatus(status: string): boolean {
  return status === "finished" || status === "completed" || status === "failed" || status === "canceled";
}

function hasResumeCheckpoint(run: RunRecord, interrupt?: RunInterrupt | null): boolean {
  return Boolean(run.last_checkpoint_id || (interrupt?.run_id === run.run_id && interrupt.checkpoint_id));
}

function isResumableRunStatus(status: string): boolean {
  return status === "paused" || status === "interrupted";
}

function stringPayloadField(payload: unknown, field: string): string {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return "";
  const value = (payload as Record<string, unknown>)[field];
  return typeof value === "string" ? value : "";
}
