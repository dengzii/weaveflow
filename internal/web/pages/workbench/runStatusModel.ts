import { stringifyJSON } from "../../lib/utils";
import type { RuntimeEvent } from "../../types";
import type { StatusTone } from "./shared";

export type EventFilterMode = "include" | "exclude";
export type ColumnRatios = [number, number, number];

export interface StoredEventFilters {
  open?: boolean;
  mode?: EventFilterMode;
  types?: string[];
  nodes?: string[];
  keyword?: string;
}

export const MIN_PANEL_HEIGHT = 180;
export const DEFAULT_COLUMN_RATIOS: ColumnRatios = [1, 1.5, 2];
export const COLUMN_SEPARATOR_WIDTH = 1;

const DEFAULT_PANEL_HEIGHT = 320;
const MIN_COLUMN_WIDTHS: ColumnRatios = [180, 260, 280];
const EVENT_FILTER_STORAGE_KEY = "weaveflow.workbench.runStatus.eventFilters";
const PANEL_HEIGHT_STORAGE_KEY = "weaveflow.workbench.runStatus.height";

export function eventListKey(event: RuntimeEvent, index: number): string {
  return `${event.id || event.run_id || "event"}-${index}`;
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
