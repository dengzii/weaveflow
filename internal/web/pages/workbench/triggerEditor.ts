import type {
  CachedGraphSummary,
  GraphDefinition,
  GraphInfo,
  Trigger,
  TriggerConcurrency,
  TriggerTarget,
  TriggerType,
  WebhookStateMapping,
} from "../../types";
import { resolveBackendUrl } from "../../lib/backend";

export interface TriggerTargetOption {
  key: string;
  label: string;
  target: TriggerTarget;
}

export interface TriggerEditorValues {
  id: string;
  name: string;
  type: TriggerType;
  enabled: boolean;
  concurrency: TriggerConcurrency;
  target: TriggerTarget;
  initialStateEntries: TriggerInitialStateEntry[];
  apiKey: string;
  mappings: WebhookStateMapping[];
  cron: string;
  timezone: string;
}

export interface TriggerInitialStateEntry {
  path: string;
  value: string;
}

export function triggerEditorValues(
  trigger: Trigger | null,
  fallbackTarget: TriggerTarget,
  initialType: TriggerType = "webhook"
): TriggerEditorValues {
  return {
    id: trigger?.id ?? "",
    name: trigger?.name ?? "",
    type: trigger?.type ?? initialType,
    enabled: trigger?.enabled ?? true,
    concurrency: trigger?.concurrency ?? "parallel",
    target: trigger?.target ?? fallbackTarget,
    initialStateEntries: triggerInitialStateEntries(trigger?.initial_state),
    apiKey: "",
    mappings: (trigger?.webhook?.state_mappings ?? []).map((mapping) => ({ ...mapping })),
    cron: trigger?.schedule?.cron ?? "*/5 * * * *",
    timezone: trigger?.schedule?.timezone ?? "UTC",
  };
}

export function buildTriggerPayload(values: TriggerEditorValues, editing: Trigger | null): Record<string, unknown> {
  const graphID = triggerTargetKey(values.target);
  if (!graphID) throw new Error("graph is required");

  const input: Record<string, unknown> = {
    name: values.name.trim() || undefined,
    type: values.type,
    enabled: values.enabled,
    concurrency: values.concurrency,
    target: { graph_id: graphID },
  };
  const initialState = buildTriggerInitialState(values.initialStateEntries);
  if (Object.keys(initialState).length > 0) input.initial_state = initialState;
  if (!editing && values.id.trim()) input.id = values.id.trim();

  if (values.type === "webhook") {
    const mappings = values.mappings
      .filter((mapping) => mapping.parameter.trim() || mapping.state_path.trim())
      .map((mapping) => ({
        parameter: mapping.parameter.trim(),
        state_path: mapping.state_path.trim(),
      }));
    if (mappings.some((mapping) => !mapping.parameter || !mapping.state_path)) {
      throw new Error("each webhook mapping requires both a parameter and state path");
    }
    input.webhook = {
      api_key: values.apiKey || undefined,
      state_mappings: mappings,
    };
  } else {
    const cron = values.cron.trim();
    if (!cron) throw new Error("cron is required");
    input.schedule = {
      cron,
      timezone: values.timezone.trim() || undefined,
      input: editing?.schedule?.input,
    };
  }
  return input;
}

export function webhookTriggerURLs(triggerID: string): { post: string; get: string } {
  const encodedID = encodeURIComponent(triggerID);
  return {
    post: withAPIKeyPlaceholder(resolveBackendUrl(`/triggers/${encodedID}`)),
    get: withAPIKeyPlaceholder(resolveBackendUrl(`/triggers/${encodedID}/webhook`)),
  };
}

function withAPIKeyPlaceholder(value: string): string {
  const url = new URL(value);
  url.searchParams.set("api_key", "YOUR_API_KEY");
  return url.toString();
}

export function buildTriggerTargetOptions(
  current: GraphInfo | null,
  cached: CachedGraphSummary[],
  preserved: TriggerTarget
): TriggerTargetOption[] {
  const result: TriggerTargetOption[] = [];
  const keys = new Set<string>();
  const add = (label: string, target: TriggerTarget) => {
    const key = triggerTargetKey(target);
    if (!key || keys.has(key)) return;
    keys.add(key);
    result.push({ key, label, target });
  };
  if (current) add(`${current.id} (current)`, { graph_id: current.id });
  for (const graph of Array.isArray(cached) ? cached : []) {
    if (graph.latest_session) add(graph.id, { graph_id: graph.id });
  }
  if (triggerTargetKey(preserved)) add(preserved.graph_id, preserved);
  return result;
}

export function defaultTriggerTarget(current: GraphInfo | null, cached: CachedGraphSummary[]): TriggerTarget {
  return buildTriggerTargetOptions(current, cached, { graph_id: "" })[0]?.target ?? { graph_id: "" };
}

export function triggerTargetKey(target?: TriggerTarget): string {
  return target?.graph_id?.trim() ?? "";
}

export function triggerTargetLabel(target?: TriggerTarget): string {
  return target?.graph_id || "server default";
}

export function buildTriggerInitialState(entries: TriggerInitialStateEntry[]): Record<string, unknown> {
  const root: Record<string, unknown> = {};
  const paths: string[] = [];
  for (const entry of entries) {
    if (!entry.path.trim() && !entry.value) continue;
    const path = normalizeInitialStatePath(entry.path);
    if (paths.some((current) => current === path)) {
      throw new Error(`duplicate initial state path ${path}`);
    }
    if (paths.some((current) => current.startsWith(`${path}.`) || path.startsWith(`${current}.`))) {
      throw new Error(`overlapping initial state path ${path}`);
    }
    paths.push(path);
    setObjectPath(root, path.split("."), parseInitialStateValue(entry.value));
  }
  return root;
}

export function triggerInitialStateEntries(initial?: Record<string, unknown>): TriggerInitialStateEntry[] {
  const entries: TriggerInitialStateEntry[] = [];
  const visit = (path: string, value: unknown) => {
    if (isJSONObject(value)) {
      for (const [name, child] of Object.entries(value)) visit(`${path}.${name}`, child);
      return;
    }
    entries.push({ path, value: formatInitialStateValue(value) });
  };
  for (const section of ["shared", "scopes"]) {
    const value = initial?.[section];
    if (!isJSONObject(value)) continue;
    for (const [name, child] of Object.entries(value)) visit(`${section}.${name}`, child);
  }
  return entries;
}

function normalizeInitialStatePath(value: string): string {
  const segments = value.split(".").map((segment) => segment.trim());
  if (segments.length < 2 || segments.some((segment) => !segment)) {
    throw new Error("initial state path must include a section and field");
  }
  if (segments[0] !== "shared" && segments[0] !== "scopes") {
    throw new Error(`initial state section ${segments[0]} is not allowed`);
  }
  if (segments[0] === "shared" && (segments[1] === "request" || segments[1] === "trigger")) {
    throw new Error(`initial state path ${segments.slice(0, 2).join(".")} is reserved`);
  }
  return segments.join(".");
}

function setObjectPath(root: Record<string, unknown>, segments: string[], value: unknown) {
  let current = root;
  for (const segment of segments.slice(0, -1)) {
    const existing = current[segment];
    const next: Record<string, unknown> = isJSONObject(existing) ? existing : {};
    if (!isJSONObject(existing)) current[segment] = next;
    current = next;
  }
  current[segments[segments.length - 1]] = value;
}

function parseInitialStateValue(value: string): unknown {
  const trimmed = value.trim();
  if (trimmed === "true") return true;
  if (trimmed === "false") return false;
  if (trimmed === "null") return null;
  if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(trimmed)) {
    const number = Number(trimmed);
    if (Number.isFinite(number)) return number;
  }
  return value;
}

function formatInitialStateValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === null || typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value);
}

function isJSONObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function triggerStatePathSuggestions(definition: GraphDefinition | null): string[] {
  const suggestions: string[] = [];
  const seen = new Set<string>();
  const addBindings = (bindings?: Record<string, { path: string }>) => {
    for (const binding of Object.values(bindings ?? {})) {
      const path = binding.path.trim();
      if (!path || seen.has(path)) continue;
      seen.add(path);
      suggestions.push(path);
    }
  };

  for (const node of definition?.nodes ?? []) addBindings(node.state);
  for (const edge of definition?.edges ?? []) addBindings(edge.condition?.state);
  return suggestions;
}
