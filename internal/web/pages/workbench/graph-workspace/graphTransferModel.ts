import { cloneJSONValue, isPlainRecord } from "../../../lib/utils";
import type {
  GraphDefinition,
  RuntimeEnvironmentPreset,
  RuntimeSettings,
  RuntimeSettingsUpdate,
} from "../../../types";

export const graphExportFormat = "weaveflow.graph-export";
export const graphExportFormatVersion = "1.0";

export type GraphExportContent = "graph" | "config" | "settings" | "ui";

export interface GraphExportBundle {
  format: typeof graphExportFormat;
  format_version: typeof graphExportFormatVersion;
  exported_at: string;
  contents: GraphExportContent[];
  graph_id: string;
  graph_version: string;
  definition: GraphDefinition;
  settings?: RuntimeSettingsUpdate;
  ui?: {
    web: Record<string, unknown>;
  };
}

export interface ParsedGraphImport {
  definition: GraphDefinition;
  graphID: string;
  graphVersion: string;
  settings?: RuntimeSettings;
  contents: GraphExportContent[];
}

export function buildGraphExportBundle({
  definition,
  graphID,
  graphVersion,
  runtimeSettings,
  includeConfig,
  includeSettings,
  includeUI,
  exportedAt = new Date().toISOString(),
}: {
  definition: GraphDefinition;
  graphID: string;
  graphVersion: string;
  runtimeSettings: RuntimeSettings;
  includeConfig: boolean;
  includeSettings: boolean;
  includeUI: boolean;
  exportedAt?: string;
}): GraphExportBundle {
  const split = splitGraphUI(definition);
  const contents: GraphExportContent[] = ["graph"];
  if (includeConfig) contents.push("config");
  if (includeSettings) contents.push("settings");
  if (includeUI) contents.push("ui");

  return {
    format: graphExportFormat,
    format_version: graphExportFormatVersion,
    exported_at: exportedAt,
    contents,
    graph_id: graphID.trim() || definition.name?.trim() || "graph",
    graph_version: graphVersion.trim() || definition.version?.trim() || "2.0",
    definition: includeConfig ? split.definition : withoutGraphConfig(split.definition),
    settings: includeSettings ? exportableRuntimeSettings(runtimeSettings) : undefined,
    ui: includeUI ? { web: includeConfig ? split.web : withoutUIConfig(split.web) } : undefined,
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

  if (value.format !== undefined) {
    if (value.format !== graphExportFormat) {
      throw new Error(`Unsupported graph export format: ${String(value.format)}`);
    }
    if (value.format_version !== graphExportFormatVersion) {
      throw new Error(`Unsupported graph export version: ${String(value.format_version)}`);
    }
    const contents = parseExportContents(value.contents);
    return parseGraphEnvelope(value, contents);
  }

  if (value.definition !== undefined) {
    const contents: GraphExportContent[] = ["graph", "config"];
    if (value.settings !== undefined) contents.push("settings");
    if (value.ui !== undefined) contents.push("ui");
    return parseGraphEnvelope(value, contents);
  }

  return {
    definition: requireGraphDefinition(value),
    graphID: stringValue(value.name),
    graphVersion: stringValue(value.version),
    contents: ["graph", "config", ...(hasGraphUI(value) ? ["ui" as const] : [])],
  };
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
  let definition = requireGraphDefinition(envelope.definition);
  const ui = envelope.ui;
  if (isPlainRecord(ui) && isPlainRecord(ui.web)) {
    definition = withGraphUI(definition, ui.web);
  }
  return {
    definition,
    graphID: stringValue(envelope.graph_id) || stringValue(definition.name),
    graphVersion: stringValue(envelope.graph_version) || stringValue(definition.version),
    settings: envelope.settings === undefined ? undefined : parseRuntimeSettings(envelope.settings),
    contents,
  };
}

function parseExportContents(value: unknown): GraphExportContent[] {
  if (!Array.isArray(value) || !value.includes("graph")) {
    throw new Error("Invalid graph export: contents must include graph.");
  }
  const allowed = new Set<GraphExportContent>(["graph", "config", "settings", "ui"]);
  if (!value.every((item) => typeof item === "string" && allowed.has(item as GraphExportContent))) {
    throw new Error("Invalid graph export: contents contains an unsupported value.");
  }
  return [...new Set(value)] as GraphExportContent[];
}

function requireGraphDefinition(value: unknown): GraphDefinition {
  if (!isPlainRecord(value) || !Array.isArray(value.nodes)) {
    throw new Error("Invalid graph file: definition.nodes must be an array.");
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

function parseRuntimeSettings(value: unknown): RuntimeSettings {
  if (!isPlainRecord(value) || !isPlainRecord(value.environment) || !Array.isArray(value.models) || !isPlainRecord(value.memory)) {
    throw new Error("Invalid graph file: runtime settings are incomplete.");
  }

  const environment: Record<string, string> = {};
  for (const [key, item] of Object.entries(value.environment)) {
    if (typeof item !== "string") {
      throw new Error(`Invalid graph file: environment ${key} must be a string.`);
    }
    environment[key] = item;
  }

  const models = value.models.map((item, index) => {
    if (!isPlainRecord(item)) {
      throw new Error(`Invalid graph file: model ${index + 1} must be an object.`);
    }
    const id = stringValue(item.id) || (index === 0 ? "default" : `model-${index + 1}`);
    const apiKey = stringValue(item.api_key);
    return {
      id,
      enabled: typeof item.enabled === "boolean" ? item.enabled : true,
      provider: stringValue(item.provider) || "openai",
      model: stringValue(item.model),
      base_url: stringValue(item.base_url),
      api_key_configured: Boolean(apiKey || item.api_key_configured === true),
      api_key: apiKey || undefined,
    };
  });

  const environmentPresets = parseEnvironmentPresets(value.environment_presets);
  return {
    environment,
    environment_presets: environmentPresets.length > 0 ? environmentPresets : undefined,
    models,
    memory: {
      enabled: typeof value.memory.enabled === "boolean" ? value.memory.enabled : false,
      directory: stringValue(value.memory.directory) || undefined,
    },
  };
}

function parseEnvironmentPresets(value: unknown): RuntimeEnvironmentPreset[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!isPlainRecord(item)) return [];
    const key = stringValue(item.key);
    const type = item.type;
    if (!key || (type !== "string" && type !== "boolean" && type !== "integer")) return [];
    return [{ key, type, default_value: stringValue(item.default_value) }];
  });
}

function exportableRuntimeSettings(settings: RuntimeSettings): RuntimeSettingsUpdate {
  const environment = Object.fromEntries(
    Object.entries(settings.environment).filter(([key]) => !isSensitiveSettingName(key))
  );
  return {
    environment,
    models: settings.models.map((model) => ({
      id: model.id,
      enabled: model.enabled,
      provider: model.provider,
      model: model.model ?? "",
      base_url: model.base_url ?? "",
    })),
    memory: {
      enabled: settings.memory.enabled,
      directory: settings.memory.directory ?? "",
    },
  };
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

function hasGraphUI(value: Record<string, unknown>): boolean {
  return isPlainRecord(value.metadata) && isPlainRecord(value.metadata.web);
}

function isSensitiveSettingName(value: string): boolean {
  const upper = value.toUpperCase();
  return upper.includes("KEY") || upper.includes("TOKEN") || upper.includes("SECRET") || upper.includes("PASSWORD");
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}
