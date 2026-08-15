import type {
  ChatChannelDefinition,
  GraphDefinition,
  Trigger,
  TriggerChatStateBindings,
  TriggerConcurrency,
  TriggerRequestStateBindings,
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
  stateBindings: TriggerEditorStateBindings;
  credentialSource: "env" | "file";
  credentialRef: string;
  mappings: WebhookStateMapping[];
  cron: string;
  timezone: string;
  streamUpdates: boolean;
  streamNodeIDs: string;
  chatChannel: string;
  chatChannelConfig: Record<string, unknown>;
  chatHistoryLimit: string;
}

export interface TriggerEditorStateBindings extends TriggerRequestStateBindings, TriggerChatStateBindings {}

export interface TriggerInitialStateEntry {
  path: string;
  value: string;
}

export function triggerEditorValues(
  trigger: Trigger | null,
  fallbackTarget: TriggerTarget,
  initialType: TriggerType = "webhook"
): TriggerEditorValues {
  const type = trigger?.type ?? initialType;
  return {
    id: trigger?.id ?? "",
    name: trigger?.name?.trim() || triggerTypeName(type),
    type,
    enabled: trigger?.enabled ?? true,
    concurrency: trigger?.concurrency ?? "parallel",
    target: trigger?.target ?? fallbackTarget,
    initialStateEntries: triggerInitialStateEntries(trigger?.initial_state),
    stateBindings: trigger ? triggerStateBindings(trigger) : defaultTriggerStateBindings(type),
    credentialSource: trigger?.credential?.source ?? "env",
    credentialRef: trigger?.credential?.ref ?? "",
    mappings: (trigger?.webhook?.state_mappings ?? []).map((mapping) => ({ ...mapping })),
    cron: trigger?.schedule?.cron ?? "*/5 * * * *",
    timezone: trigger?.schedule?.timezone ?? "UTC",
    streamUpdates: trigger?.chat?.stream_updates ?? true,
    streamNodeIDs: (trigger?.chat?.stream_node_ids ?? []).join(", "),
    chatChannel: trigger?.chat?.channel ?? "http",
    chatChannelConfig: cloneRecord(trigger?.chat?.channel_config),
    chatHistoryLimit: trigger?.chat?.history_limit ? String(trigger.chat.history_limit) : "",
  };
}

export function buildTriggerPayload(
  values: TriggerEditorValues,
  editing: Trigger | null,
  chatSetupSessionID?: string
): Record<string, unknown> {
  const graphID = triggerTargetKey(values.target);
  if (!graphID) throw new Error("graph is required");

  const input: Record<string, unknown> = {
    id: values.id.trim(),
    name: values.name.trim() || triggerTypeName(values.type),
    type: values.type,
    enabled: values.enabled,
    concurrency: values.concurrency,
  };
  const credentialRef = values.credentialRef.trim();
  if (credentialRef) {
    input.credential = { source: values.credentialSource, ref: credentialRef };
  }
  const initialState = buildTriggerInitialState(values.initialStateEntries);
  if (Object.keys(initialState).length > 0) input.initial_state = initialState;
  if (!values.id.trim()) throw new Error("trigger id is required");

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
    mappings.forEach((mapping, index) => {
      mapping.state_path = normalizeTriggerStatePath(mapping.state_path, `webhook mapping ${index + 1}`);
    });
    const stateBindings = buildTriggerStateBindings("webhook", values.stateBindings);
    validateStateDestinationOverlaps([
      ...stateBindingDestinations("webhook", stateBindings),
      ...mappings.map((mapping, index) => ({
        label: `webhook mapping ${index + 1}`,
        path: mapping.state_path,
        effectivePath: mapping.state_path,
      })),
      ...initialStateDestinations(values.initialStateEntries),
    ]);
    input.webhook = {
      state_bindings: Object.keys(stateBindings).length > 0 ? stateBindings : undefined,
      state_mappings: mappings,
    };
  } else if (values.type === "schedule") {
    const cron = values.cron.trim();
    if (!cron) throw new Error("cron is required");
    const stateBindings = buildTriggerStateBindings("schedule", values.stateBindings);
    validateStateDestinationOverlaps([
      ...stateBindingDestinations("schedule", stateBindings),
      ...initialStateDestinations(values.initialStateEntries),
    ]);
    input.schedule = {
      cron,
      timezone: values.timezone.trim() || undefined,
      input: editing?.schedule?.input,
      state_bindings: Object.keys(stateBindings).length > 0 ? stateBindings : undefined,
    };
  } else {
    const streamNodeIDs = Array.from(new Set(
      values.streamNodeIDs.split(/[\s,]+/).map((value) => value.trim()).filter(Boolean)
    ));
    const chat: Record<string, unknown> = {
      channel: values.chatChannel.trim() || "http",
      channel_config: cloneRecord(values.chatChannelConfig),
      stream_updates: values.streamUpdates,
      stream_node_ids: streamNodeIDs,
    };
    const historyLimit = parseChatHistoryLimit(values.chatHistoryLimit);
    if (historyLimit > 0) chat.history_limit = historyLimit;
    const stateBindings = buildTriggerStateBindings("chat", values.stateBindings);
    validateStateDestinationOverlaps([
      ...stateBindingDestinations("chat", stateBindings),
      ...initialStateDestinations(values.initialStateEntries),
    ]);
    if (Object.keys(stateBindings).length > 0) chat.state_bindings = stateBindings;
    input.chat = chat;
    if (chatSetupSessionID?.trim()) input.chat_setup_session_id = chatSetupSessionID.trim();
  }
  return input;
}

export function triggerDraftFromEditorValues(
  values: TriggerEditorValues,
  current: Trigger | null,
  timestamp = new Date().toISOString()
): Trigger {
  let initialState = current?.initial_state;
  try {
    const nextInitialState = buildTriggerInitialState(values.initialStateEntries);
    initialState = Object.keys(nextInitialState).length > 0 ? nextInitialState : undefined;
  } catch {
    initialState = current?.initial_state;
  }

  const trigger: Trigger = {
    id: values.id.trim(),
    name: values.name.trim() || triggerTypeName(values.type),
    type: values.type,
    enabled: values.enabled,
    concurrency: values.concurrency,
    target: { graph_id: values.target.graph_id.trim() },
    credential: values.credentialRef.trim()
      ? { source: values.credentialSource, ref: values.credentialRef.trim() }
      : undefined,
    initial_state: initialState,
    created_at: current?.created_at || timestamp,
    updated_at: current?.updated_at || timestamp,
  };

  if (values.type === "webhook") {
    const stateBindings = draftStateBindings("webhook", values.stateBindings);
    trigger.webhook = {
      state_mappings: values.mappings.map((mapping) => ({ ...mapping })),
      ...(Object.keys(stateBindings).length > 0 ? { state_bindings: stateBindings } : {}),
    };
  } else if (values.type === "schedule") {
    const stateBindings = draftStateBindings("schedule", values.stateBindings);
    trigger.schedule = {
      cron: values.cron,
      timezone: values.timezone,
      input: current?.schedule?.input,
      ...(Object.keys(stateBindings).length > 0 ? { state_bindings: stateBindings } : {}),
    };
  } else {
    const historyLimit = /^\d+$/.test(values.chatHistoryLimit.trim())
      ? Number(values.chatHistoryLimit.trim())
      : undefined;
    const stateBindings = draftStateBindings("chat", values.stateBindings);
    trigger.chat = {
      channel: values.chatChannel,
      channel_config: cloneRecord(values.chatChannelConfig),
      stream_updates: values.streamUpdates,
      stream_node_ids: values.streamNodeIDs.split(/[\s,]+/).map((value) => value.trim()).filter(Boolean),
      history_limit: historyLimit,
      ...(Object.keys(stateBindings).length > 0 ? { state_bindings: stateBindings } : {}),
    };
  }
  return trigger;
}

export function triggerTypeName(type: TriggerType): string {
  if (type === "webhook") return "Webhook";
  if (type === "schedule") return "Schedule";
  return "Chat";
}

function parseChatHistoryLimit(value: string): number {
  const trimmed = value.trim();
  if (!trimmed) return 0;
  if (!/^\d+$/.test(trimmed)) {
    throw new Error("chat history rounds must be an integer between 0 and 500");
  }
  const limit = Number(trimmed);
  if (!Number.isSafeInteger(limit) || limit > 500) {
    throw new Error("chat history rounds must be an integer between 0 and 500");
  }
  return limit;
}

interface StateBindingField {
  key: keyof TriggerEditorStateBindings;
  label: string;
  conversation?: boolean;
}

interface StateDestination {
  label: string;
  path: string;
  effectivePath: string;
}

const requestStateBindingFields: StateBindingField[] = [
  { key: "input", label: "input" },
  { key: "metadata", label: "metadata" },
  { key: "trigger_id", label: "trigger ID" },
  { key: "trigger_type", label: "trigger type" },
  { key: "raw_body", label: "raw body" },
];

const chatStateBindingFields: StateBindingField[] = [
  { key: "input", label: "input" },
  { key: "conversation", label: "conversation", conversation: true },
  { key: "raw_history", label: "raw history" },
  { key: "trigger_id", label: "trigger ID" },
  { key: "channel", label: "channel" },
  { key: "user_id", label: "user ID" },
  { key: "conversation_id", label: "conversation ID" },
  { key: "message_id", label: "message ID" },
];

function triggerStateBindings(trigger: Trigger): TriggerEditorStateBindings {
  if (trigger.type === "webhook") return { ...(trigger.webhook?.state_bindings ?? {}) };
  if (trigger.type === "schedule") return { ...(trigger.schedule?.state_bindings ?? {}) };
  return { ...(trigger.chat?.state_bindings ?? {}) };
}

function defaultTriggerStateBindings(type: TriggerType): TriggerEditorStateBindings {
  if (type === "chat") return { input: "shared.request.input" };
  return {
    input: "shared.request.input",
    metadata: "shared.request.metadata",
    trigger_id: "shared.trigger.id",
    trigger_type: "shared.trigger.type",
  };
}

function stateBindingFields(type: TriggerType): StateBindingField[] {
  return type === "chat" ? chatStateBindingFields : requestStateBindingFields;
}

function buildTriggerStateBindings(
  type: TriggerType,
  source: TriggerEditorStateBindings
): TriggerEditorStateBindings {
  const bindings: TriggerEditorStateBindings = {};
  for (const field of stateBindingFields(type)) {
    const path = normalizeTriggerStatePath(source[field.key] ?? "", field.label);
    if (path) bindings[field.key] = path;
  }
  return bindings;
}

function draftStateBindings(type: TriggerType, source: TriggerEditorStateBindings): TriggerEditorStateBindings {
  const bindings: TriggerEditorStateBindings = {};
  for (const field of stateBindingFields(type)) {
    const path = source[field.key]?.trim();
    if (path) bindings[field.key] = path;
  }
  return bindings;
}

function stateBindingDestinations(
  type: TriggerType,
  bindings: TriggerEditorStateBindings
): StateDestination[] {
  return stateBindingFields(type).flatMap((field) => {
    const path = bindings[field.key];
    if (!path) return [];
    return [{
      label: field.label,
      path,
      effectivePath: field.conversation ? `${path}.messages` : path,
    }];
  });
}

function initialStateDestinations(entries: TriggerInitialStateEntry[]): StateDestination[] {
  return entries.flatMap((entry) => {
    if (!entry.path.trim() && !entry.value) return [];
    const path = normalizeInitialStatePath(entry.path);
    return [{ label: "initial state", path, effectivePath: path }];
  });
}

function validateStateDestinationOverlaps(destinations: StateDestination[]) {
  const configured: StateDestination[] = [];
  for (const destination of destinations) {
    const previous = configured.find((entry) => statePathsOverlap(destination.effectivePath, entry.effectivePath));
    if (previous) {
      throw new Error(
        `${destination.label} state path ${destination.path} overlaps ${previous.label} state path ${previous.path}`
      );
    }
    configured.push(destination);
  }
}

function normalizeTriggerStatePath(value: string, label: string): string {
  const trimmed = value.trim();
  if (!trimmed) return "";
  const segments = trimmed.split(".").map((segment) => segment.trim());
  if (segments.length < 2 || segments.some((segment) => !segment)) {
    throw new Error(`${label} state path must include a section and field`);
  }
  if (segments[0] !== "shared" && segments[0] !== "scopes") {
    throw new Error(`${label} state section ${segments[0]} is not allowed`);
  }
  return segments.join(".");
}

function statePathsOverlap(left: string, right: string): boolean {
  return left === right || left.startsWith(`${right}.`) || right.startsWith(`${left}.`);
}

export function editableChatChannelSchema(
  definition: ChatChannelDefinition | undefined,
  preserveWriteOnly: boolean
): Record<string, unknown> | undefined {
  if (!definition) return undefined;
  const schema = cloneRecord(definition.config_schema);
  placeSchemaPropertyAfter(schema, "secret", "bot_id");
  if (preserveWriteOnly) removeWriteOnlyRequirements(schema);
  return schema;
}

export function chatChannelDefaultConfig(definition: ChatChannelDefinition | undefined): Record<string, unknown> {
  if (!definition || !isJSONObject(definition.config_schema.properties)) return {};
  const config: Record<string, unknown> = {};
  for (const [key, rawProperty] of Object.entries(definition.config_schema.properties)) {
    if (!isJSONObject(rawProperty)) continue;
    if (Object.prototype.hasOwnProperty.call(rawProperty, "default")) {
      config[key] = cloneJSONValue(rawProperty.default);
      continue;
    }
    if (rawProperty.type === "object") {
      const nested = chatChannelDefaultConfig({ id: key, title: key, config_schema: rawProperty });
      if (Object.keys(nested).length > 0) config[key] = nested;
    }
  }
  return config;
}

function removeWriteOnlyRequirements(schema: Record<string, unknown>) {
  const properties = isJSONObject(schema.properties) ? schema.properties : {};
  if (Array.isArray(schema.required)) {
    schema.required = schema.required.filter((key) => {
      if (typeof key !== "string") return true;
      const property = properties[key];
      return !isJSONObject(property) || property.writeOnly !== true;
    });
  }
  for (const property of Object.values(properties)) {
    if (isJSONObject(property)) removeWriteOnlyRequirements(property);
  }
}

function placeSchemaPropertyAfter(schema: Record<string, unknown>, propertyName: string, anchorName: string) {
  if (!isJSONObject(schema.properties)) return;
  const properties = schema.properties;
  if (
    !Object.prototype.hasOwnProperty.call(properties, propertyName)
    || !Object.prototype.hasOwnProperty.call(properties, anchorName)
  ) return;

  const property = properties[propertyName];
  const reorderedProperties: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(properties)) {
    if (key === propertyName) continue;
    reorderedProperties[key] = value;
    if (key === anchorName) reorderedProperties[propertyName] = property;
  }
  schema.properties = reorderedProperties;
}

function cloneRecord(value: unknown): Record<string, unknown> {
  if (!isJSONObject(value)) return {};
  return JSON.parse(JSON.stringify(value)) as Record<string, unknown>;
}

function cloneJSONValue(value: unknown): unknown {
  return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
}

export function webhookTriggerURL(graphID: string, triggerID: string): string {
  return resolveBackendUrl(
    `/graphs/${encodeURIComponent(graphID)}/triggers/${encodeURIComponent(triggerID)}/webhook`
  );
}

export function webhookCurlCommand(url: string): string {
  return `curl -X POST "${url}" -H "Authorization: Bearer $TRIGGER_TOKEN" -H "Content-Type: application/json" -d "{}"`;
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
