import { stringifyJSON } from "../../lib/utils";
import type { CheckpointRecord, RunRecord, RuntimeEvent, StepRecord } from "../../types";
import type { StatusTone } from "./shared";
import { runDurationMilliseconds } from "./workbenchRunModel";

export type EventFilterMode = "include" | "exclude";
export type ColumnRatios = [number, number, number];
export type StateChangeKind = "added" | "updated" | "removed" | "changed";
export type StateHistoryKind = "baseline" | "change" | "barrier";

export interface StateHistoryChange {
  path: string;
  kind: StateChangeKind;
}

export interface StateHistoryEntry {
  kind: StateHistoryKind;
  checkpointID: string;
  checkpoint?: CheckpointRecord;
  event?: RuntimeEvent;
  changes: StateHistoryChange[];
  nodeID: string;
  stepID: string;
  timestamp: string;
}

export interface StoredEventFilters {
  open?: boolean;
  mode?: EventFilterMode;
  types?: string[];
  nodes?: string[];
  keyword?: string;
}

export interface RunMetricsSummary {
  durationMs: number;
  eventCount: number;
  stepCount: number;
  succeededSteps: number;
  failedSteps: number;
  activeSteps: number;
  retries: number;
  checkpointCount: number;
  stateChangeCount: number;
  llmCallCount: number;
  toolCallCount: number;
  toolFailureCount: number;
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cachedPromptTokens: number;
  warningCount: number;
  errorCount: number;
}

export interface RunIOCheckpoints {
  input?: CheckpointRecord;
  output?: CheckpointRecord;
}

export const MIN_PANEL_HEIGHT = 180;
export const DEFAULT_COLUMN_RATIOS: ColumnRatios = [1, 1.5, 2];
export const COLUMN_SEPARATOR_WIDTH = 1;
export const EVENT_ROW_HEIGHT = 28;
export const EVENT_ROW_OVERSCAN = 10;

const DEFAULT_PANEL_HEIGHT = 320;
const MIN_COLUMN_WIDTHS: ColumnRatios = [180, 260, 280];
const EVENT_FILTER_STORAGE_KEY = "weaveflow.workbench.runStatus.eventFilters";
const PANEL_HEIGHT_STORAGE_KEY = "weaveflow.workbench.runStatus.height";

export function eventListKey(event: RuntimeEvent, index: number): string {
  if (event.id) return `event-${event.id}`;
  return `event-${event.run_id || "run"}-${event.type}-${event.timestamp}-${event.node_id ?? ""}-${event.step_id ?? ""}-${index}`;
}

export function fixedVirtualRange(
  itemCount: number,
  scrollTop: number,
  viewportHeight: number,
  rowHeight: number,
  overscan: number
): { start: number; end: number; offset: number } {
  if (itemCount <= 0 || rowHeight <= 0) return { start: 0, end: 0, offset: 0 };
  const safeScrollTop = Math.max(0, Number.isFinite(scrollTop) ? scrollTop : 0);
  const safeViewportHeight = Math.max(0, Number.isFinite(viewportHeight) ? viewportHeight : 0);
  const safeOverscan = Math.max(0, Math.floor(overscan));
  const start = Math.min(
    itemCount - 1,
    Math.max(0, Math.floor(safeScrollTop / rowHeight) - safeOverscan)
  );
  const visibleEnd = Math.ceil((safeScrollTop + safeViewportHeight) / rowHeight);
  const end = Math.min(itemCount, Math.max(start + 1, visibleEnd + safeOverscan));
  return { start, end, offset: start * rowHeight };
}

export function stateHistoryEntries(
  events: RuntimeEvent[],
  steps: StepRecord[] = [],
  checkpoints: CheckpointRecord[] = []
): StateHistoryEntry[] {
  const sortedCheckpoints = [...checkpoints].sort(compareCheckpointTime);
  const checkpointsByID = new Map(sortedCheckpoints.map((checkpoint) => [checkpoint.checkpoint_id, checkpoint]));
  const afterCheckpointByStepID = new Map(
    sortedCheckpoints
      .filter((checkpoint) => checkpoint.stage === "after_node" && checkpoint.step_id)
      .map((checkpoint) => [checkpoint.step_id, checkpoint])
  );
  const stepByID = new Map(steps.map((step) => [step.step_id, step]));
  const entries: StateHistoryEntry[] = [];
  const baseline = sortedCheckpoints.find((checkpoint) => checkpoint.stage === "before_node");

  if (baseline) {
    entries.push(checkpointHistoryEntry("baseline", baseline));
  }
  for (const checkpoint of sortedCheckpoints) {
    if (checkpoint.stage === "after_parallel_wave") {
      entries.push(checkpointHistoryEntry("barrier", checkpoint));
    }
  }
  for (const event of events) {
    if (event.type !== "state.changed") continue;
    const step = event.step_id ? stepByID.get(event.step_id) : undefined;
    const checkpoint = step?.checkpoint_after_id
      ? checkpointsByID.get(step.checkpoint_after_id)
      : event.step_id
        ? afterCheckpointByStepID.get(event.step_id)
        : undefined;
    entries.push({
      kind: "change",
      checkpointID: checkpoint?.checkpoint_id ?? step?.checkpoint_after_id ?? "",
      checkpoint,
      event,
      changes: stateChanges(event.payload),
      nodeID: event.node_id ?? checkpoint?.node_id ?? "",
      stepID: event.step_id ?? checkpoint?.step_id ?? "",
      timestamp: event.timestamp,
    });
  }
  return entries.sort((left, right) => {
    const timestampOrder = timeRank(right.timestamp) - timeRank(left.timestamp);
    if (timestampOrder !== 0) return timestampOrder;
    return stateHistoryIdentity(left).localeCompare(stateHistoryIdentity(right));
  });
}

function checkpointHistoryEntry(
  kind: Exclude<StateHistoryKind, "change">,
  checkpoint: CheckpointRecord
): StateHistoryEntry {
  return {
    kind,
    checkpointID: checkpoint.checkpoint_id,
    checkpoint,
    changes: [],
    nodeID: checkpoint.node_id,
    stepID: checkpoint.step_id,
    timestamp: checkpoint.created_at,
  };
}

function compareCheckpointTime(left: CheckpointRecord, right: CheckpointRecord): number {
  const timestampOrder = timeRank(left.created_at) - timeRank(right.created_at);
  return timestampOrder !== 0 ? timestampOrder : left.checkpoint_id.localeCompare(right.checkpoint_id);
}

function stateHistoryIdentity(entry: StateHistoryEntry): string {
  return entry.checkpointID || entry.event?.id || `${entry.kind}-${entry.nodeID}-${entry.stepID}`;
}

function stateChanges(payload: unknown): StateHistoryChange[] {
  if (!isRecord(payload) || !Array.isArray(payload.changes)) return [];
  return payload.changes.map((change, index) => {
    if (!isRecord(change)) {
      return { path: `change ${index + 1}`, kind: "changed" };
    }
    return {
      path: typeof change.path === "string" && change.path.trim() ? change.path.trim() : `change ${index + 1}`,
      kind: stateChangeKind(change),
    };
  });
}

function stateChangeKind(change: Record<string, unknown>): StateChangeKind {
  const hasBefore = Object.prototype.hasOwnProperty.call(change, "before");
  const hasAfter = Object.prototype.hasOwnProperty.call(change, "after");
  if (hasBefore && hasAfter) return "updated";
  if (hasAfter) return "added";
  if (hasBefore) return "removed";
  return "changed";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function readStoredPanelHeight(): number {
  if (typeof window === "undefined") return DEFAULT_PANEL_HEIGHT;
  try {
    const raw = window.localStorage.getItem(PANEL_HEIGHT_STORAGE_KEY);
    const parsed = raw ? Number(raw) : NaN;
    if (!Number.isFinite(parsed)) return DEFAULT_PANEL_HEIGHT;
    const maxHeight = Math.max(MIN_PANEL_HEIGHT, window.innerHeight - 160);
    return Math.max(MIN_PANEL_HEIGHT, Math.min(maxHeight, parsed));
  } catch {
    return DEFAULT_PANEL_HEIGHT;
  }
}

export function writeStoredPanelHeight(height: number): void {
  if (typeof window === "undefined" || !Number.isFinite(height)) return;
  try {
    window.localStorage.setItem(PANEL_HEIGHT_STORAGE_KEY, String(Math.round(height)));
  } catch {
    // Storage is optional; resizing still works for the current session.
  }
}

export function readStoredEventFilters(): StoredEventFilters {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(EVENT_FILTER_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as StoredEventFilters;
    return {
      open: typeof parsed.open === "boolean" ? parsed.open : false,
      mode: parsed.mode === "exclude" ? "exclude" : "include",
      types: Array.isArray(parsed.types) ? parsed.types.filter(isStringValue) : [],
      nodes: Array.isArray(parsed.nodes) ? parsed.nodes.filter(isStringValue) : [],
      keyword: typeof parsed.keyword === "string" ? parsed.keyword : "",
    };
  } catch {
    return {};
  }
}

export function writeStoredEventFilters(filters: StoredEventFilters): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(EVENT_FILTER_STORAGE_KEY, JSON.stringify(filters));
  } catch {
    // Storage is optional; filtering still works for the current session.
  }
}

function isStringValue(value: unknown): value is string {
  return typeof value === "string";
}

export function eventMatchesFilters(
  event: RuntimeEvent,
  filters: { mode: EventFilterMode; types: string[]; nodes: string[]; keyword: string }
): boolean {
  if (!hasEventFilterCriteria(filters)) return true;
  const matches = eventMatchesPositiveFilters(event, filters);
  return filters.mode === "exclude" ? !matches : matches;
}

export function summarizeRunMetrics(
  run: RunRecord | undefined,
  steps: StepRecord[] = [],
  checkpoints: CheckpointRecord[] = [],
  events: RuntimeEvent[] = [],
  now = Date.now()
): RunMetricsSummary {
  const runEvents = run ? events.filter((event) => event.run_id === run.run_id) : [];
  const runSteps = run ? steps.filter((step) => step.run_id === run.run_id) : [];
  const runCheckpoints = run ? checkpoints.filter((checkpoint) => checkpoint.run_id === run.run_id) : [];
  const durationMs = runDurationMilliseconds(run, now);
  let promptTokens = 0;
  let completionTokens = 0;
  let reasoningTokens = 0;
  let cachedPromptTokens = 0;
  let llmCallCount = 0;
  let toolCallCount = 0;
  let toolFailureCount = 0;
  let stateChangeCount = 0;
  let warningCount = 0;
  let errorCount = 0;

  for (const event of runEvents) {
    const payload = recordPayload(event.payload);
    if (event.type === "llm.call") {
      llmCallCount += Math.max(1, numberPayload(payload, "calls"));
      promptTokens += numberPayload(payload, "prompt_tokens");
      completionTokens += numberPayload(payload, "completion_tokens");
      reasoningTokens += numberPayload(payload, "reasoning_tokens");
      cachedPromptTokens += numberPayload(payload, "prompt_cached_tokens");
    }
    if (event.type === "tool.called") toolCallCount += Math.max(1, numberPayload(payload, "count"));
    if (event.type === "tool.failed") {
      toolFailureCount += 1;
    }
    if (event.type === "state.changed") stateChangeCount += payloadArrayLength(payload, "changes");
    if (event.type === "warning") warningCount += 1;
    if (event.type.includes("failed") || event.type === "contract.violation") errorCount += 1;
  }

  return {
    durationMs,
    eventCount: runEvents.length,
    stepCount: runSteps.length,
    succeededSteps: runSteps.filter((step) => step.status === "succeeded").length,
    failedSteps: runSteps.filter((step) => step.status === "failed").length,
    activeSteps: runSteps.filter((step) => step.status === "running" || step.status === "scheduled").length,
    retries: runSteps.reduce((total, step) => total + Math.max(0, step.attempt - 1), 0),
    checkpointCount: runCheckpoints.length,
    stateChangeCount,
    llmCallCount,
    toolCallCount,
    toolFailureCount,
    promptTokens,
    completionTokens,
    reasoningTokens,
    cachedPromptTokens,
    warningCount,
    errorCount,
  };
}

export function selectRunIOCheckpoints(checkpoints: CheckpointRecord[] = []): RunIOCheckpoints {
  const sorted = [...checkpoints].sort(compareCheckpointTime);
  const input = sorted.find((checkpoint) => checkpoint.stage === "before_node") ?? sorted[0];
  const finalCheckpoints = sorted.filter((checkpoint) => checkpoint.stage === "final");
  const output = finalCheckpoints.at(-1) ?? sorted.at(-1);
  return { input, output };
}

function recordPayload(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function numberPayload(payload: Record<string, unknown> | null, key: string): number {
  const value = payload?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function payloadArrayLength(payload: Record<string, unknown> | null, key: string): number {
  return Array.isArray(payload?.[key]) ? payload[key].length : 0;
}

function eventMatchesPositiveFilters(
  event: RuntimeEvent,
  filters: { types: string[]; nodes: string[]; keyword: string }
): boolean {
  if (filters.types.length > 0 && !filters.types.includes(event.type)) return false;
  const nodeID = event.node_id ?? "";
  if (filters.nodes.length > 0 && !filters.nodes.includes(nodeID)) return false;
  const keyword = filters.keyword.trim().toLowerCase();
  if (!keyword) return true;
  return eventSearchText(event).includes(keyword);
}

function hasEventFilterCriteria(filters: { types: string[]; nodes: string[]; keyword: string }): boolean {
  return filters.types.length > 0 || filters.nodes.length > 0 || filters.keyword.trim() !== "";
}

export function toggleFilterValue(values: string[], value: string): string[] {
  return values.includes(value) ? values.filter((item) => item !== value) : [...values, value];
}

function eventSearchText(event: RuntimeEvent): string {
  return [
    event.id,
    event.run_id,
    event.step_id,
    event.node_id,
    event.type,
    event.timestamp,
    event.payload === undefined ? "" : stringifyJSON(event.payload),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

export function uniqueSorted(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))].sort((left, right) =>
    left.localeCompare(right)
  );
}

export function eventTone(type: string): StatusTone {
  if (type.includes("failed") || type.includes("error")) return "danger";
  if (type.includes("finished") || type.includes("succeeded") || type.includes("completed")) return "ok";
  if (type.includes("paused")) return "warn";
  if (type.includes("started") || type.includes("running")) return "live";
  return "neutral";
}

export function timeRank(value?: string): number {
  if (!value) return 0;
  const timestamp = Date.parse(value);
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

export function resizeRunPanelColumnRatios(
  current: ColumnRatios,
  boundary: 0 | 1,
  deltaPixels: number,
  availableWidth: number
): ColumnRatios {
  if (!Number.isFinite(deltaPixels) || !Number.isFinite(availableWidth) || availableWidth <= 0) return current;
  const totalRatio = current[0] + current[1] + current[2];
  if (totalRatio <= 0) return current;

  const widths: ColumnRatios = [
    (availableWidth * current[0]) / totalRatio,
    (availableWidth * current[1]) / totalRatio,
    (availableWidth * current[2]) / totalRatio,
  ];
  const leftIndex = boundary;
  const rightIndex = boundary + 1;
  const pairWidth = widths[leftIndex] + widths[rightIndex];
  const pairMinimum = MIN_COLUMN_WIDTHS[leftIndex] + MIN_COLUMN_WIDTHS[rightIndex];
  const minimumScale = Math.min(1, pairWidth / pairMinimum);
  const leftMinimum = MIN_COLUMN_WIDTHS[leftIndex] * minimumScale;
  const rightMinimum = MIN_COLUMN_WIDTHS[rightIndex] * minimumScale;
  const clampedDelta = Math.max(
    leftMinimum - widths[leftIndex],
    Math.min(widths[rightIndex] - rightMinimum, deltaPixels)
  );

  widths[leftIndex] += clampedDelta;
  widths[rightIndex] -= clampedDelta;
  return [
    (widths[0] / availableWidth) * totalRatio,
    (widths[1] / availableWidth) * totalRatio,
    (widths[2] / availableWidth) * totalRatio,
  ];
}

export function columnGridTemplate(ratios: ColumnRatios): string {
  return `minmax(0, ${ratios[0]}fr) ${COLUMN_SEPARATOR_WIDTH}px minmax(0, ${ratios[1]}fr) ${COLUMN_SEPARATOR_WIDTH}px minmax(0, ${ratios[2]}fr)`;
}

export function columnBoundaryPercent(ratios: ColumnRatios, boundary: 0 | 1): number {
  const total = ratios[0] + ratios[1] + ratios[2];
  const occupied = boundary === 0 ? ratios[0] : ratios[0] + ratios[1];
  return total > 0 ? Math.round((occupied / total) * 100) : 0;
}
