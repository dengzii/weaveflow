import { createGraphID, graphDefinitionVersion } from "../../../lib/graphEditor";
import { defaultGraphVersion } from "../../../lib/localGraphs";
import { cloneJSONValue, isPlainRecord } from "../../../lib/utils";
import type {
  GraphDefinition,
  RuntimeEnvironmentPreset,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  Trigger,
} from "../../../types";

export const graphExportFormat = "weaveflow.graph-export";
export const graphExportFormatVersion = "1.0";

export type GraphExportContent = "graph" | "config" | "settings" | "triggers" | "ui";
export type GraphExportTrigger = Omit<Trigger, "target" | "created_at" | "updated_at">;
export type GraphImportStrategy = "copy" | "overwrite";

export interface GraphExportBundle {
  format: typeof graphExportFormat;
  format_version: typeof graphExportFormatVersion;
  exported_at: string;
  contents: GraphExportContent[];
  graph_id: string;
  graph_version: string;
  definition: GraphDefinition;
  settings?: RuntimeSettingsUpdate;
  triggers?: GraphExportTrigger[];
  ui?: {
    web: Record<string, unknown>;
  };
}

export interface ParsedGraphImport {
  definition: GraphDefinition;
  graphID: string;
  graphVersion: string;
  settings?: RuntimeSettings;
  triggers?: Trigger[];
  contents: GraphExportContent[];
}

export function buildGraphExportBundle({
  definition,
  graphID,
  graphVersion,
  runtimeSettings,
  triggers,
  includeConfig,
  includeSettings,
  includeTriggers,
  includeUI,
  exportedAt = new Date().toISOString(),
}: {
  definition: GraphDefinition;
  graphID: string;
  graphVersion: string;
  runtimeSettings: RuntimeSettings;
  triggers: Trigger[];
  includeConfig: boolean;
  includeSettings: boolean;
  includeTriggers: boolean;
  includeUI: boolean;
  exportedAt?: string;
}): GraphExportBundle {
  const split = splitGraphUI(definition);
  const contents: GraphExportContent[] = ["graph"];
  if (includeConfig) contents.push("config");
  if (includeSettings) contents.push("settings");
  if (includeTriggers) contents.push("triggers");
  if (includeUI) contents.push("ui");
  const exportedTriggers = includeTriggers ? exportableTriggers(triggers) : undefined;

  return {
    format: graphExportFormat,
    format_version: graphExportFormatVersion,
    exported_at: exportedAt,
    contents,
    graph_id: graphID.trim() || definition.name?.trim() || "graph",
    graph_version: graphVersion.trim() || defaultGraphVersion,
    definition: includeConfig ? split.definition : withoutGraphConfig(split.definition),
    settings: includeSettings ? exportableRuntimeSettings(runtimeSettings) : undefined,
    triggers: exportedTriggers,
    ui: includeUI ? {
      web: exportableGraphUI(
        includeConfig ? split.web : withoutUIConfig(split.web),
        exportedTriggers?.map((trigger) => trigger.id) ?? []
      ),
    } : undefined,
  };
}

export function resolveGraphImport(
  graph: ParsedGraphImport,
  {
    strategy,
    existingGraphIDs,
    existingGraphNames,
    generatedGraphID = createGraphID(),
  }: {
    strategy: GraphImportStrategy;
    existingGraphIDs: string[];
    existingGraphNames: string[];
    existingTriggerIDs?: string[];
    generatedGraphID?: string;
  }
): ParsedGraphImport {
  const usedGraphIDs = new Set(existingGraphIDs.map((value) => value.trim()).filter(Boolean));
  const usedGraphNames = new Set(existingGraphNames.map((value) => value.trim()).filter(Boolean));
  const sourceGraphID = graph.graphID.trim() || graph.definition.name?.trim() || "imported_graph";
  const sourceGraphName = graph.definition.name?.trim() || sourceGraphID;
  if (strategy === "overwrite") {
    if (!usedGraphIDs.has(sourceGraphID)) {
      throw new Error(`Cannot overwrite missing graph ${sourceGraphID}.`);
    }
    return {
      ...graph,
      graphID: sourceGraphID,
      triggers: graph.triggers?.map((trigger) => ({
        ...trigger,
        target: { graph_id: sourceGraphID },
      })),
      definition: {
        ...graph.definition,
        name: sourceGraphName,
      },
    };
  }
  const graphID = nextAvailableValue(generatedGraphID.trim() || createGraphID(), usedGraphIDs, "_");
  const graphName = nextAvailableValue(sourceGraphName, usedGraphNames, " ");
  const triggers = graph.triggers?.map((trigger) => {
    return {
      ...trigger,
      target: { graph_id: graphID },
    };
  });

  return {
    ...graph,
    graphID,
    triggers,
    definition: {
      ...graph.definition,
      name: graphName,
    },
  };
}

export function parseGraphImport(text: string): ParsedGraphImport {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`Invalid JSON: ${message}`);
  }
  if (!isPlainRecord(value)) {
    throw new Error("Invalid graph file: expected a JSON object.");
  }

  if (value.format !== graphExportFormat) {
    throw new Error(`Unsupported graph export format: ${String(value.format ?? "missing")}`);
  }
  if (value.format_version !== graphExportFormatVersion) {
    throw new Error(`Unsupported graph export version: ${String(value.format_version)}`);
  }
  const contents = parseExportContents(value.contents);
  validateDeclaredContents(value, contents);
  return parseGraphEnvelope(value, contents);
}

export function graphExportFilename(graphID: string, definition: GraphDefinition): string {
  const source = graphID.trim() || definition.name?.trim() || "graph";
  const safe = source
    .replace(/[<>:"/\\|?*\u0000-\u001f]/g, "-")
    .replace(/[. ]+$/g, "")
    .trim();
  return `${safe || "graph"}.weaveflow.json`;
}

function parseGraphEnvelope(
  envelope: Record<string, unknown>,
  contents: GraphExportContent[]
): ParsedGraphImport {
  let definition = parseImportedGraphDefinition(envelope.definition);
  const ui = envelope.ui;
  if (isPlainRecord(ui) && isPlainRecord(ui.web)) {
    definition = withGraphUI(definition, ui.web);
  }
  return {
    definition,
    graphID: stringValue(envelope.graph_id) || stringValue(definition.name),
    graphVersion: stringValue(envelope.graph_version) || defaultGraphVersion,
    settings: envelope.settings === undefined ? undefined : parseRuntimeSettings(envelope.settings),
    triggers: envelope.triggers === undefined
      ? undefined
      : parseGraphTriggers(
          envelope.triggers,
          stringValue(envelope.graph_id) || stringValue(definition.name)
        ),
    contents,
  };
}

function parseExportContents(value: unknown): GraphExportContent[] {
  if (!Array.isArray(value) || !value.includes("graph")) {
    throw new Error("Invalid graph export: contents must include graph.");
  }
  const allowed = new Set<GraphExportContent>(["graph", "config", "settings", "triggers", "ui"]);
  if (!value.every((item) => typeof item === "string" && allowed.has(item as GraphExportContent))) {
    throw new Error("Invalid graph export: contents contains an unsupported value.");
  }
  const declared = new Set(value as GraphExportContent[]);
  return (["graph", "config", "settings", "triggers", "ui"] as const)
    .filter((content) => declared.has(content));
}

function validateDeclaredContents(envelope: Record<string, unknown>, contents: GraphExportContent[]) {
  assertKnownFields(envelope, [
    "format",
    "format_version",
    "exported_at",
    "contents",
    "graph_id",
    "graph_version",
    "definition",
    "settings",
    "triggers",
    "ui",
  ], "graph export");
  if (!stringValue(envelope.exported_at)) throw new Error("Invalid graph export: exported_at is required.");
  if (!stringValue(envelope.graph_id)) throw new Error("Invalid graph export: graph_id is required.");
  if (!stringValue(envelope.graph_version)) throw new Error("Invalid graph export: graph_version is required.");
  const declared = new Set(contents);
  for (const field of ["settings", "triggers", "ui"] as const) {
    const hasField = envelope[field] !== undefined;
    if (declared.has(field) && !hasField) {
      throw new Error(`Invalid graph export: contents declares ${field}, but ${field} is missing.`);
    }
    if (!declared.has(field) && hasField) {
      throw new Error(`Invalid graph export: ${field} is present but contents does not declare it.`);
    }
  }
  if (!isPlainRecord(envelope.definition)) return;
  const definition = envelope.definition;
  const hiddenUI = isPlainRecord(definition.metadata) && definition.metadata.web !== undefined;
  if (hiddenUI) {
    throw new Error("Invalid graph export: definition.metadata.web must be declared through ui.");
  }
  if (!declared.has("config") && graphDefinitionHasConfig(definition)) {
    throw new Error("Invalid graph export: config is present but contents does not declare it.");
  }
}

function graphDefinitionHasConfig(definition: Record<string, unknown>): boolean {
  const nodes = Array.isArray(definition.nodes) ? definition.nodes : [];
  if (nodes.some((node) => isPlainRecord(node) && node.config !== undefined)) return true;
  const edges = Array.isArray(definition.edges) ? definition.edges : [];
  return edges.some((edge) => isPlainRecord(edge) && isPlainRecord(edge.condition) && edge.condition.config !== undefined);
}

function requireGraphDefinition(value: unknown): GraphDefinition {
  if (!isPlainRecord(value) || !Array.isArray(value.nodes)) {
    throw new Error("Invalid graph file: definition.nodes must be an array.");
  }
  if (!Array.isArray(value.state_modules) || value.state_modules.length === 0) {
    throw new Error("Invalid graph file: definition.state_modules must be a non-empty array.");
  }
  for (const [index, node] of value.nodes.entries()) {
    if (!isPlainRecord(node) || typeof node.id !== "string" || !node.id.trim()) {
      throw new Error(`Invalid graph file: node ${index + 1} must have an id.`);
    }
  }
  if (value.edges !== undefined && !Array.isArray(value.edges)) {
    throw new Error("Invalid graph file: definition.edges must be an array.");
  }
  return cloneJSONValue(value) as unknown as GraphDefinition;
}

function parseImportedGraphDefinition(value: unknown): GraphDefinition {
  if (!isPlainRecord(value)) {
    throw new Error("Invalid graph file: definition must be an object.");
  }
  const version = stringValue(value.version);
  if (version !== graphDefinitionVersion) {
    throw new Error(`Unsupported Graph Definition version: ${version || "missing"}`);
  }
  return requireGraphDefinition(value);
}

function parseRuntimeSettings(value: unknown): RuntimeSettings {
  if (!isPlainRecord(value) || !isPlainRecord(value.environment) || !Array.isArray(value.models)) {
    throw new Error("Invalid graph file: runtime settings are incomplete.");
  }

  const environment: Record<string, string> = {};
  for (const [key, item] of Object.entries(value.environment)) {
    if (typeof item !== "string") {
      throw new Error(`Invalid graph file: environment ${key} must be a string.`);
    }
    environment[key] = item;
  }
  assertKnownFields(value, [
    "environment",
    "environment_secrets",
    "environment_presets",
    "models",
    "tool_permissions",
    "tool_approvals",
  ], "runtime settings");

  const environmentSecrets = parseSecretRefs(value.environment_secrets, "environment secret");

  const models = value.models.map((item, index) => {
    if (!isPlainRecord(item)) {
      throw new Error(`Invalid graph file: model ${index + 1} must be an object.`);
    }
    const id = stringValue(item.id) || (index === 0 ? "default" : `model-${index + 1}`);
    assertKnownFields(item, [
      "id",
      "enabled",
      "provider",
      "api_format",
      "model",
      "base_url",
      "extra_body",
      "pricing",
    ], `model ${index + 1}`);
    return {
      id,
      enabled: typeof item.enabled === "boolean" ? item.enabled : true,
      provider: stringValue(item.provider) || "openai",
      api_format: stringValue(item.api_format) || "chat_completions",
      model: stringValue(item.model),
      base_url: stringValue(item.base_url),
      extra_body: isPlainRecord(item.extra_body) ? cloneJSONValue(item.extra_body) : undefined,
      pricing: parseModelPricing(item.pricing),
      credential_configured: false,
    };
  });

  const environmentPresets = parseEnvironmentPresets(value.environment_presets);
  return {
    environment,
    environment_secrets: environmentSecrets,
    environment_presets: environmentPresets.length > 0 ? environmentPresets : undefined,
    models,
    tool_permissions: stringArray(value.tool_permissions),
    tool_approvals: booleanRecord(value.tool_approvals),
  };
}

function parseModelPricing(value: unknown) {
  if (!isPlainRecord(value)) return undefined;
  const input = finiteNonNegativeNumber(value.input_per_million);
  const cachedInput = finiteNonNegativeNumber(value.cached_input_per_million);
  const output = finiteNonNegativeNumber(value.output_per_million);
  if (input === 0 && cachedInput === 0 && output === 0) return undefined;
  return {
    currency: stringValue(value.currency).toUpperCase() || "USD",
    input_per_million: input,
    cached_input_per_million: cachedInput,
    output_per_million: output,
  };
}

function finiteNonNegativeNumber(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : 0;
}

function stringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => typeof item === "string" && item.trim() ? [item.trim()] : []);
}

function booleanRecord(value: unknown): Record<string, boolean> {
  if (!isPlainRecord(value)) return {};
  return Object.fromEntries(Object.entries(value).flatMap(([key, item]) => (
    key.trim() && typeof item === "boolean" ? [[key.trim(), item]] : []
  )));
}

function parseEnvironmentPresets(value: unknown): RuntimeEnvironmentPreset[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!isPlainRecord(item)) return [];
    const key = stringValue(item.key);
    const type = item.type;
    if (!key || (type !== "string" && type !== "boolean" && type !== "integer")) return [];
    return [{ key, type, default_value: stringValue(item.default_value), secret: item.secret === true }];
  });
}

function parseSecretRefs(value: unknown, label: string): Record<string, { source: "env" | "file"; ref: string }> {
  if (value === undefined) return {};
  if (!isPlainRecord(value)) throw new Error(`Invalid graph file: ${label}s must be an object.`);
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, requireSecretRef(item, `${label} ${key}`)]));
}

function parseSecretRef(value: unknown, label: string) {
  if (value === undefined) return undefined;
  return requireSecretRef(value, label);
}

function requireSecretRef(value: unknown, label: string): { source: "env" | "file"; ref: string } {
  if (!isPlainRecord(value)) throw new Error(`Invalid graph file: ${label} must be an object.`);
  const source = stringValue(value.source);
  const ref = stringValue(value.ref);
  if ((source !== "env" && source !== "file") || !ref) {
    throw new Error(`Invalid graph file: ${label} requires source env or file and a ref.`);
  }
  return { source, ref };
}

function exportableRuntimeSettings(settings: RuntimeSettings): RuntimeSettingsUpdate {
  return {
    environment: Object.fromEntries(
      Object.entries(settings.environment).filter(([key]) => !isSensitiveSettingName(key))
    ),
    environment_secrets: Object.fromEntries(
      Object.entries(settings.environment_secrets).filter(([, ref]) => ref.source !== "managed")
    ),
    models: settings.models.map((model) => ({
      id: model.id,
      enabled: model.enabled,
      provider: model.provider,
      api_format: model.api_format || "chat_completions",
      model: model.model ?? "",
      base_url: model.base_url ?? "",
      extra_body: model.extra_body ? cloneJSONValue(model.extra_body) : undefined,
      pricing: model.pricing ? cloneJSONValue(model.pricing) : undefined,
    })),
    tool_permissions: [...settings.tool_permissions],
    tool_approvals: { ...settings.tool_approvals },
  };
}

function exportableTriggers(triggers: Trigger[]): GraphExportTrigger[] {
  return triggers.map((trigger) => {
    const item: GraphExportTrigger = {
      id: trigger.id,
      name: trigger.name,
      type: trigger.type,
      enabled: false,
      concurrency: trigger.concurrency,
      credential: trigger.credential && trigger.credential.source !== "managed"
        ? cloneJSONValue(trigger.credential)
        : undefined,
      initial_state: cloneJSONValue(trigger.initial_state),
      webhook: trigger.webhook ? {
        state_bindings: cloneJSONValue(trigger.webhook.state_bindings),
        state_mappings: cloneJSONValue(trigger.webhook.state_mappings),
      } : undefined,
      schedule: cloneJSONValue(trigger.schedule),
      chat: trigger.chat ? {
        ...cloneJSONValue(trigger.chat),
        channel_config: sanitizeSensitiveValues(trigger.chat.channel_config),
      } : undefined,
    };
    return item;
  });
}

function parseGraphTriggers(value: unknown, graphID: string): Trigger[] {
  if (!Array.isArray(value)) {
    throw new Error("Invalid graph file: triggers must be an array.");
  }
  const usedIDs = new Set<string>();
  return value.map((item, index) => {
    if (!isPlainRecord(item)) {
      throw new Error(`Invalid graph file: trigger ${index + 1} must be an object.`);
    }
    const id = stringValue(item.id);
    if (!id || usedIDs.has(id)) {
      throw new Error(`Invalid graph file: trigger ${index + 1} must have a unique id.`);
    }
    usedIDs.add(id);
    const type = item.type;
    if (type !== "webhook" && type !== "schedule" && type !== "chat") {
      throw new Error(`Invalid graph file: trigger ${id} has an unsupported type.`);
    }
    const concurrency = item.concurrency;
    if (concurrency !== undefined && concurrency !== "parallel" && concurrency !== "skip") {
      throw new Error(`Invalid graph file: trigger ${id} has an unsupported concurrency policy.`);
    }
    assertKnownFields(item, [
      "id",
      "name",
      "type",
      "enabled",
      "concurrency",
      "credential",
      "initial_state",
      "webhook",
      "schedule",
      "chat",
    ], `trigger ${index + 1}`);
    if (isPlainRecord(item.webhook) && Object.prototype.hasOwnProperty.call(item.webhook, "api_key")) {
      throw new Error(`Invalid graph file: trigger ${id} uses the removed plaintext webhook api_key field.`);
    }
    return {
      ...(cloneJSONValue(item) as unknown as GraphExportTrigger),
      id,
      type,
      enabled: typeof item.enabled === "boolean" ? item.enabled : true,
      concurrency: concurrency ?? "parallel",
      credential: parseSecretRef(item.credential, `trigger ${id} credential`),
      target: { graph_id: graphID },
      created_at: "",
      updated_at: "",
    };
  });
}

function exportableGraphUI(web: Record<string, unknown>, triggerIDs: string[]): Record<string, unknown> {
  const next = cloneJSONValue(web);
  if (!isPlainRecord(next.trigger_nodes)) return next;
  const validTriggerIDs = new Set(triggerIDs);
  next.trigger_nodes = Object.fromEntries(
    Object.entries(next.trigger_nodes).filter(([triggerID]) => validTriggerIDs.has(triggerID))
  );
  if (Object.keys(next.trigger_nodes).length === 0) delete next.trigger_nodes;
  return next;
}

function sanitizeSensitiveValues(value: Record<string, unknown> | undefined): Record<string, unknown> | undefined {
  if (!value) return undefined;
  const sanitized = Object.fromEntries(
    Object.entries(value).flatMap(([key, item]) => {
      if (isSensitiveSettingName(key) || key.toLowerCase() === "bot_id") return [];
      if (isPlainRecord(item)) return [[key, sanitizeSensitiveValues(item)]];
      if (Array.isArray(item)) {
        return [[key, item.map((entry) => isPlainRecord(entry) ? sanitizeSensitiveValues(entry) : entry)]];
      }
      return [[key, item]];
    })
  );
  return sanitized;
}

function nextAvailableValue(base: string, used: Set<string>, separator: string): string {
  if (!used.has(base)) return base;
  for (let index = 1; index < 1000; index += 1) {
    const candidate = `${base}${separator}${index}`;
    if (!used.has(candidate)) return candidate;
  }
  return `${base}${separator}${Date.now().toString(36)}`;
}

function withoutGraphConfig(definition: GraphDefinition): GraphDefinition {
  return {
    ...definition,
    nodes: definition.nodes.map((node) => {
      const next = { ...node };
      delete next.config;
      return next;
    }),
    edges: definition.edges?.map((edge) => {
      if (!edge.condition) return { ...edge };
      const condition = { ...edge.condition };
      delete condition.config;
      return { ...edge, condition };
    }),
  };
}

function withoutUIConfig(web: Record<string, unknown>): Record<string, unknown> {
  const next = cloneJSONValue(web);
  if (!Array.isArray(next.virtual_edges)) return next;
  next.virtual_edges = next.virtual_edges.map((edge) => {
    if (!isPlainRecord(edge) || !isPlainRecord(edge.condition)) return edge;
    const condition = { ...edge.condition };
    delete condition.config;
    return { ...edge, condition };
  });
  return next;
}

function splitGraphUI(definition: GraphDefinition): {
  definition: GraphDefinition;
  web: Record<string, unknown>;
} {
  const next = cloneJSONValue(definition);
  const metadata = isPlainRecord(next.metadata) ? { ...next.metadata } : {};
  const web = isPlainRecord(metadata.web) ? cloneJSONValue(metadata.web) : {};
  delete metadata.web;
  if (Object.keys(metadata).length > 0) next.metadata = metadata;
  else delete next.metadata;
  return { definition: next, web };
}

function withGraphUI(definition: GraphDefinition, web: Record<string, unknown>): GraphDefinition {
  return {
    ...definition,
    metadata: {
      ...(definition.metadata ?? {}),
      web: cloneJSONValue(web),
    },
  };
}

function isSensitiveSettingName(value: string): boolean {
  const upper = value.toUpperCase();
  return upper.includes("KEY") || upper.includes("TOKEN") || upper.includes("SECRET") || upper.includes("PASSWORD");
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function assertKnownFields(value: Record<string, unknown>, allowed: readonly string[], label: string) {
  const allowedFields = new Set(allowed);
  const unknown = Object.keys(value).find((key) => !allowedFields.has(key));
  if (unknown) throw new Error(`Invalid graph file: ${label} contains unknown field ${unknown}.`);
}
