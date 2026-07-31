import { cloneJSONValue, isPlainRecord, stringifyJSON } from "../../../lib/utils";

const INITIAL_STATE_SECTIONS = ["shared", "scopes", "internal", "runtime"];
const UNSAFE_PATH_SEGMENTS = new Set(["__proto__", "prototype", "constructor"]);

export interface ParsedInitialState {
  root: Record<string, unknown>;
  error: string | null;
}

export function parseInitialStateText(text: string): ParsedInitialState {
  try {
    const parsed = JSON.parse(text) as unknown;
    return {
      root: normalizeInitialStateRoot(parsed),
      error: null,
    };
  } catch (err) {
    return {
      root: normalizeInitialStateRoot({}),
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

export function normalizeInitialStateRoot(value: unknown): Record<string, unknown> {
  const root = isPlainRecord(value) ? { ...value } : {};
  for (const section of INITIAL_STATE_SECTIONS) {
    if (!isPlainRecord(root[section])) root[section] = {};
  }
  return root;
}

export function updateInitialStatePath(currentText: string, path: string, value: unknown): Record<string, unknown> {
  const root = parseInitialStateText(currentText).root;
  setPathValue(root, path, value);
  return root;
}

export function getPathValue(root: unknown, path: string): unknown {
  const result = pathValue(root, path);
  return result.exists ? result.value : undefined;
}

export function hasFilledInitialStatePath(root: unknown, path: string, type?: string): boolean {
  const result = pathValue(root, path);
  return result.exists && hasFilledRequirementValue(result.value, type);
}

export function hasFilledRequirementValue(value: unknown, type?: string): boolean {
  if (value === null || value === undefined) return false;

  switch ((type ?? "").trim().toLowerCase()) {
    case "string":
      return typeof value === "string" && value.trim().length > 0;
    case "boolean":
    case "bool":
      return typeof value === "boolean";
    case "number":
    case "float":
    case "float64":
    case "integer":
    case "int":
    case "int64":
      return typeof value === "number" && Number.isFinite(value);
    case "object":
    case "map":
      return isPlainRecord(value);
    case "array":
    case "list":
      return Array.isArray(value);
    default:
      return typeof value !== "string" || value.trim().length > 0;
  }
}

export function parseStatePath(path: string): string[] | null {
  const segments = path.split(".").map((part) => part.trim()).filter(Boolean);
  if (segments.length === 0 || segments.some((segment) => UNSAFE_PATH_SEGMENTS.has(segment))) return null;
  return segments;
}

export function formatJSONFieldValue(value: unknown): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  return stringifyJSON(value);
}

function pathValue(root: unknown, path: string): { exists: boolean; value?: unknown } {
  const segments = parseStatePath(path);
  if (!segments) return { exists: false };

  let current = root;
  for (const segment of segments) {
    if (!isPlainRecord(current) || !Object.prototype.hasOwnProperty.call(current, segment)) {
      return { exists: false };
    }
    current = current[segment];
  }
  return { exists: true, value: current };
}

function setPathValue(root: Record<string, unknown>, path: string, value: unknown) {
  const segments = parseStatePath(path);
  if (!segments) return;

  let current = root;
  for (const segment of segments.slice(0, -1)) {
    const next = current[segment];
    if (!isPlainRecord(next)) current[segment] = {};
    current = current[segment] as Record<string, unknown>;
  }
  current[segments[segments.length - 1]] = cloneJSONValue(value);
}
