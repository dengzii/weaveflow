import type { CheckpointRecord, RunInspection, RuntimeEvent } from "../../types";

export const MAX_CACHED_RUN_INSPECTIONS = 6;

export function rememberRunInspection(
  current: ReadonlyMap<string, RunInspection>,
  inspection: RunInspection,
  maximumSize = MAX_CACHED_RUN_INSPECTIONS
): Map<string, RunInspection> {
  const next = new Map(current);
  next.delete(inspection.run.run_id);
  next.set(inspection.run.run_id, inspection);
  while (next.size > maximumSize) {
    const oldestRunID = next.keys().next().value;
    if (typeof oldestRunID !== "string") break;
    next.delete(oldestRunID);
  }
  return next;
}

export function checkpointRecordFromEvent(event: RuntimeEvent): CheckpointRecord | null {
  if (event.type !== "checkpoint.created" || !event.run_id || !isRecord(event.payload)) return null;
  const checkpointID = stringField(event.payload, "checkpoint_id");
  const stage = stringField(event.payload, "stage");
  if (!checkpointID || !stage) return null;
  return {
    checkpoint_id: checkpointID,
    run_id: event.run_id,
    step_id: event.step_id ?? "",
    node_id: event.node_id ?? "",
    stage,
    state_codec: "",
    state_version: "",
    created_at: event.timestamp,
  };
}

export function upsertCheckpoint(
  current: CheckpointRecord[],
  checkpoint: CheckpointRecord
): CheckpointRecord[] {
  const existingIndex = current.findIndex((item) => item.checkpoint_id === checkpoint.checkpoint_id);
  const next = existingIndex >= 0
    ? current.map((item, index) => (index === existingIndex ? { ...checkpoint, ...item } : item))
    : [...current, checkpoint];
  return next.sort(compareCheckpointCreatedAt);
}

export function mergeFetchedCheckpoints(
  current: CheckpointRecord[],
  fetched: CheckpointRecord[]
): CheckpointRecord[] {
  const checkpointsByID = new Map(current.map((checkpoint) => [checkpoint.checkpoint_id, checkpoint]));
  for (const checkpoint of fetched) {
    const existing = checkpointsByID.get(checkpoint.checkpoint_id);
    checkpointsByID.set(checkpoint.checkpoint_id, existing ? { ...existing, ...checkpoint } : checkpoint);
  }
  return [...checkpointsByID.values()].sort(compareCheckpointCreatedAt);
}

function compareCheckpointCreatedAt(left: CheckpointRecord, right: CheckpointRecord): number {
  return Date.parse(left.created_at) - Date.parse(right.created_at);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  return typeof value === "string" ? value.trim() : "";
}
