import type {
  CachedGraphSummary,
  ChatChannelDefinition,
  GraphDefinition,
  GraphInfo,
  Trigger,
  TriggerChatStateBindings,
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
  streamUpdates: boolean;
  streamNodeIDs: string;
  chatChannel: string;
  chatChannelConfig: Record<string, unknown>;
  chatHistoryLimit: string;
  chatConversationStatePath: string;
  chatRawHistoryStatePath: string;
  chatTriggerIDStatePath: string;
  chatChannelStatePath: string;
  chatUserIDStatePath: string;
  chatConversationIDStatePath: string;
  chatMessageIDStatePath: string;
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
  const type = trigger?.type ?? initialType;
  return {
    id: trigger?.id ?? "",
    name: trigger?.name?.trim() || triggerTypeName(type),
    type,
    enabled: trigger?.enabled ?? true,
    concurrency: trigger?.concurrency ?? "parallel",
    target: trigger?.target ?? fallbackTarget,
    initialStateEntries: triggerInitialStateEntries(trigger?.initial_state),
    apiKey: "",
    mappings: (trigger?.webhook?.state_mappings ?? []).map((mapping) => ({ ...mapping })),
    cron: trigger?.schedule?.cron ?? "*/5 * * * *",
    timezone: trigger?.schedule?.timezone ?? "UTC",
    streamUpdates: trigger?.chat?.stream_updates ?? true,
    streamNodeIDs: (trigger?.chat?.stream_node_ids ?? []).join(", "),
    chatChannel: trigger?.chat?.channel ?? "http",
    chatChannelConfig: cloneRecord(trigger?.chat?.channel_config),
    chatHistoryLimit: trigger?.chat?.history_limit ? String(trigger.chat.history_limit) : "",
    chatConversationStatePath: trigger?.chat?.state_bindings?.conversation ?? "",
    chatRawHistoryStatePath: trigger?.chat?.state_bindings?.raw_history ?? "",
    chatTriggerIDStatePath: trigger?.chat?.state_bindings?.trigger_id ?? "",
    chatChannelStatePath: trigger?.chat?.state_bindings?.channel ?? "",
    chatUserIDStatePath: trigger?.chat?.state_bindings?.user_id ?? "",
    chatConversationIDStatePath: trigger?.chat?.state_bindings?.conversation_id ?? "",
    chatMessageIDStatePath: trigger?.chat?.state_bindings?.message_id ?? "",
  };
}

export function buildTriggerPayload(
  values: TriggerEditorValues,
  editing: Trigger | null,
  chatSetupSessionID?: string,
  creating = !editing
): Record<string, unknown> {
  const graphID = triggerTargetKey(values.target);
  if (!graphID) throw new Error("graph is required");

  const input: Record<string, unknown> = {
    name: editing ? (editing.name?.trim() || triggerTypeName(editing.type)) : values.name.trim(),
    type: values.type,
    enabled: values.enabled,
    concurrency: values.concurrency,
    target: { graph_id: graphID },
  };
  const initialState = buildTriggerInitialState(values.initialStateEntries);
  if (Object.keys(initialState).length > 0) input.initial_state = initialState;
  if (creating && values.id.trim()) input.id = values.id.trim();

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
  } else if (values.type === "schedule") {
    const cron = values.cron.trim();
    if (!cron) throw new Error("cron is required");
    input.schedule = {
      cron,
      timezone: values.timezone.trim() || undefined,
      input: editing?.schedule?.input,
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
    const stateBindings = buildChatStateBindings(values);
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
    initial_state: initialState,
    created_at: current?.created_at || timestamp,
    updated_at: current?.updated_at || timestamp,
  };

  if (values.type === "webhook") {
    trigger.webhook = {
      api_key: values.apiKey || undefined,
      state_mappings: values.mappings.map((mapping) => ({ ...mapping })),
    };
  } else if (values.type === "schedule") {
    trigger.schedule = {
      cron: values.cron,
      timezone: values.timezone,
      input: current?.schedule?.input,
    };
  } else {
    const historyLimit = /^\d+$/.test(values.chatHistoryLimit.trim())
      ? Number(values.chatHistoryLimit.trim())
      : undefined;
    trigger.chat = {
      channel: values.chatChannel,
      channel_config: cloneRecord(values.chatChannelConfig),
      stream_updates: values.streamUpdates,
      stream_node_ids: values.streamNodeIDs.split(/[\s,]+/).map((value) => value.trim()).filter(Boolean),
      history_limit: historyLimit,
      state_bindings: {
        conversation: values.chatConversationStatePath,
        raw_history: values.chatRawHistoryStatePath,
        trigger_id: values.chatTriggerIDStatePath,
        channel: values.chatChannelStatePath,
        user_id: values.chatUserIDStatePath,
        conversation_id: values.chatConversationIDStatePath,
        message_id: values.chatMessageIDStatePath,
      },
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

function buildChatStateBindings(values: TriggerEditorValues): TriggerChatStateBindings {
  const fields: Array<{ key: keyof TriggerChatStateBindings; label: string; value: string; conversation?: boolean }> = [
    { key: "conversation", label: "conversation", value: values.chatConversationStatePath, conversation: true },
    { key: "raw_history", label: "raw history", value: values.chatRawHistoryStatePath },
    { key: "trigger_id", label: "trigger ID", value: values.chatTriggerIDStatePath },
    { key: "channel", label: "channel", value: values.chatChannelStatePath },
    { key: "user_id", label: "user ID", value: values.chatUserIDStatePath },
    { key: "conversation_id", label: "conversation ID", value: values.chatConversationIDStatePath },
    { key: "message_id", label: "message ID", value: values.chatMessageIDStatePath },
  ];
  const bindings: TriggerChatStateBindings = {};
  const configured: Array<{ label: string; path: string }> = [];
  for (const field of fields) {
    const path = normalizeChatStatePath(field.value, field.label);
    if (!path) continue;
    const effectivePath = field.conversation ? `${path}.messages` : path;
    if (statePathsOverlap(effectivePath, "shared.request.input")) {
      throw new Error(`${field.label} state path ${path} overlaps the chat input path`);
    }
    const previous = configured.find((entry) => statePathsOverlap(effectivePath, entry.path));
    if (previous) {
      throw new Error(`${field.label} state path ${path} overlaps ${previous.label} state path ${previous.path}`);
    }
    bindings[field.key] = path;
    configured.push({ label: field.label, path: effectivePath });
  }
  return bindings;
}

function normalizeChatStatePath(value: string, label: string): string {
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

export function chatTriggerURL(triggerID: string): string {
  return resolveBackendUrl(`/triggers/${encodeURIComponent(triggerID)}/chat`);
}

export function webhookTriggerURLs(triggerID: string): { post: string; get: string } {
  const encodedID = encodeURIComponent(triggerID);
  return {
		post: withAPIKeyPlaceholder(resolveBackendUrl(`/triggers/${encodedID}/invocations`)),
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
