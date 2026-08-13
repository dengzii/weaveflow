import { createGraphID } from "../../../lib/graphEditor";
import { cloneJSONValue, isPlainRecord } from "../../../lib/utils";
import type {
  ConditionSchema,
  GraphDefinition,
  GraphNodeSpec,
  NodeTypeSchema,
  RegistryInfo,
  RuntimeEnvironmentPreset,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  StateBinding,
  Trigger,
} from "../../../types";

export const graphExportFormat = "weaveflow.graph-export";
export const graphExportFormatVersion = "1.0";

export type GraphExportContent = "graph" | "config" | "settings" | "triggers" | "ui";
export type GraphExportTrigger = Omit<Trigger, "target" | "created_at" | "updated_at">;

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
  migration?: GraphImportMigration;
}

export interface GraphImportMigration {
  sourceVersion: string;
  targetVersion: string;
  warnings: string[];
}

const currentGraphDefinitionVersion = "2.0";
const legacyGraphDefinitionVersion = "1.0";
const legacyStateSchema = "weaveflow.state.v2";
const protocolsStateModule = { name: "weaveflow.protocols", version: "1" };
const legacyDefaultScope = "agent";

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
    graph_version: graphVersion.trim() || definition.version?.trim() || "2.0",
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

export function resolveGraphImportConflicts(
  graph: ParsedGraphImport,
  existingGraphIDs: string[],
  existingGraphNames: string[],
  existingTriggerIDs: string[] = [],
  generatedGraphID = createGraphID()
): ParsedGraphImport {
  const usedGraphIDs = new Set(existingGraphIDs.map((value) => value.trim()).filter(Boolean));
  const usedGraphNames = new Set(existingGraphNames.map((value) => value.trim()).filter(Boolean));
  const sourceGraphID = graph.graphID.trim() || graph.definition.name?.trim() || "imported_graph";
  const sourceGraphName = graph.definition.name?.trim() || sourceGraphID;
  const graphID = nextAvailableValue(generatedGraphID.trim() || createGraphID(), usedGraphIDs, "_");
  const graphName = nextAvailableValue(sourceGraphName, usedGraphNames, " ");
  const usedTriggerIDs = new Set(existingTriggerIDs.map((value) => value.trim()).filter(Boolean));
  const triggerIDMap = new Map<string, string>();
  const triggers = graph.triggers?.map((trigger) => {
    const triggerID = nextAvailableValue(trigger.id, usedTriggerIDs, "_");
    usedTriggerIDs.add(triggerID);
    triggerIDMap.set(trigger.id, triggerID);
    return {
      ...trigger,
      id: triggerID,
      target: { graph_id: graphID },
    };
  });

  return {
    ...graph,
    graphID,
    triggers,
    definition: {
      ...remapTriggerCanvasPositions(graph.definition, triggerIDMap),
      name: graphName,
    },
  };
}

export function parseGraphImport(text: string, registry: RegistryInfo | null = null): ParsedGraphImport {
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
    return parseGraphEnvelope(value, contents, registry);
  }

  if (value.definition !== undefined) {
    const contents: GraphExportContent[] = ["graph", "config"];
    if (value.settings !== undefined) contents.push("settings");
    if (value.triggers !== undefined) contents.push("triggers");
    if (value.ui !== undefined) contents.push("ui");
    return parseGraphEnvelope(value, contents, registry);
  }

  const parsedDefinition = parseImportedGraphDefinition(value, registry);
  return {
    definition: parsedDefinition.definition,
    graphID: stringValue(value.name),
    graphVersion: stringValue(parsedDefinition.definition.version),
    contents: ["graph", "config", ...(hasGraphUI(value) ? ["ui" as const] : [])],
    migration: parsedDefinition.migration,
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
  contents: GraphExportContent[],
  registry: RegistryInfo | null
): ParsedGraphImport {
  const parsedDefinition = parseImportedGraphDefinition(envelope.definition, registry);
  let definition = parsedDefinition.definition;
  const ui = envelope.ui;
  if (isPlainRecord(ui) && isPlainRecord(ui.web)) {
    definition = withGraphUI(definition, ui.web);
  }
  return {
    definition,
    graphID: stringValue(envelope.graph_id) || stringValue(definition.name),
    graphVersion: stringValue(envelope.graph_version) || stringValue(definition.version),
    settings: envelope.settings === undefined ? undefined : parseRuntimeSettings(envelope.settings),
    triggers: envelope.triggers === undefined
      ? undefined
      : parseGraphTriggers(
          envelope.triggers,
          stringValue(envelope.graph_id) || stringValue(definition.name)
        ),
    contents,
    migration: parsedDefinition.migration,
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

function parseImportedGraphDefinition(
  value: unknown,
  registry: RegistryInfo | null
): { definition: GraphDefinition; migration?: GraphImportMigration } {
  if (!isPlainRecord(value)) {
    throw new Error("Invalid graph file: definition must be an object.");
  }
  const version = stringValue(value.version);
  if (version === currentGraphDefinitionVersion) {
    return { definition: requireGraphDefinition(value) };
  }
  if (version === legacyGraphDefinitionVersion || (!version && value.state_modules === undefined)) {
    if (!registry) {
      throw new Error("Cannot migrate Graph Definition 1.0 before the current registry is loaded.");
    }
    return migrateGraphDefinitionV1(value, registry);
  }
  throw new Error(`Unsupported Graph Definition version: ${version || "missing"}`);
}

function migrateGraphDefinitionV1(
  value: Record<string, unknown>,
  registry: RegistryInfo
): { definition: GraphDefinition; migration: GraphImportMigration } {
  const legacyDefinition = requireGraphDefinition(value);
  const stateSchema = stringValue(value.state_schema) || legacyStateSchema;
  if (stateSchema !== legacyStateSchema) {
    throw new Error(`Cannot migrate Graph Definition 1.0 state_schema ${JSON.stringify(stateSchema)} automatically.`);
  }
  if (!registry.state_modules.some((module) => (
    module.name === protocolsStateModule.name && module.version === protocolsStateModule.version
  ))) {
    throw new Error(`Cannot migrate Graph Definition 1.0: current registry does not provide ${protocolsStateModule.name}@${protocolsStateModule.version}.`);
  }

  const warnings: string[] = [];
  const nodes = legacyDefinition.nodes.map((node, index) => migrateGraphNodeV1(node, index, registry, warnings));
  const edges = legacyDefinition.edges?.map((edge, index) => {
    if (!edge.condition) return cloneJSONValue(edge);
    return {
      ...cloneJSONValue(edge),
      condition: migrateGraphConditionV1(edge.condition as unknown as Record<string, unknown>, index, registry, warnings),
    };
  });
  const definition: GraphDefinition = {
    version: currentGraphDefinitionVersion,
    name: legacyDefinition.name,
    description: legacyDefinition.description,
    state_modules: [protocolsStateModule],
    entry_point: legacyDefinition.entry_point,
    finish_point: legacyDefinition.finish_point,
    nodes,
    edges,
    metadata: legacyDefinition.metadata ? cloneJSONValue(legacyDefinition.metadata) : undefined,
  };
  return {
    definition,
    migration: {
      sourceVersion: legacyGraphDefinitionVersion,
      targetVersion: currentGraphDefinitionVersion,
      warnings,
    },
  };
}

function migrateGraphNodeV1(
  source: GraphNodeSpec,
  index: number,
  registry: RegistryInfo,
  warnings: string[]
): GraphNodeSpec {
  const nodeID = stringValue(source.id);
  const sourceType = stringValue(source.type);
  const config = isPlainRecord(source.config) ? cloneJSONValue(source.config) : {};
  const scope = legacyScope(config);
  let targetType = sourceType;
  let state: Record<string, StateBinding> = {};

  switch (sourceType) {
    case "human_message": {
      const content = stringValue(config.content);
      if (!content) {
        throw new Error(`Cannot migrate node ${JSON.stringify(nodeID)}: human_message without fixed content requires a user_input plus conversation_message topology.`);
      }
      targetType = "conversation_message";
      state = { conversation: { path: legacyConversationPath(scope) } };
      config.role = "human";
      delete config.interrupt_message;
      delete config.state_scope;
      break;
    }
    case "llm":
      targetType = "llm_turn";
      state = { conversation: { path: legacyConversationPath(scope) } };
      delete config.state_scope;
      break;
    case "tools":
      targetType = "tool_execution";
      state = { conversation: { path: legacyConversationPath(scope) } };
      delete config.state_scope;
      break;
    case "agent":
      state = {
        task: { path: stringValue(config.input_path) || "shared.request.input" },
        conversation: { path: legacyConversationPath(scope) },
        result: { path: stringValue(config.output_path) || "shared.final.answer" },
      };
      delete config.state_scope;
      delete config.input_path;
      delete config.output_path;
      if (config.tool_name !== undefined || config.tool_description !== undefined) {
        warnings.push(`Node ${JSON.stringify(nodeID)} dropped obsolete agent tool_name/tool_description config.`);
      }
      delete config.tool_name;
      delete config.tool_description;
      break;
    case "context_reducer":
      state = { conversation: { path: legacyConversationPath(scope) } };
      delete config.state_scope;
      break;
    case "environment_context":
      state = { environment: { path: stringValue(config.environment_state_path) || "shared.environment" } };
      delete config.environment_state_path;
      break;
    case "mapped_subgraph":
      throw new Error(`Cannot migrate node ${JSON.stringify(nodeID)}: mapped_subgraph has no semantics-preserving current equivalent.`);
    default:
      throw new Error(`Cannot migrate node ${JSON.stringify(nodeID)}: unsupported legacy node type ${JSON.stringify(sourceType)}.`);
  }

  const nodeType = requireRegisteredNodeType(targetType, nodeID, index, registry);
  state = withDefaultStateBindings(state, nodeType, nodeID);
  if (targetType !== sourceType) {
    warnings.push(`Node ${JSON.stringify(nodeID)} type migrated from ${sourceType} to ${targetType}.`);
  }
  return {
    id: nodeID,
    name: source.name,
    type: targetType,
    description: source.description,
    config: Object.keys(config).length > 0 ? config : undefined,
    state: Object.keys(state).length > 0 ? state : undefined,
  };
}

function requireRegisteredNodeType(
  nodeType: string,
  nodeID: string,
  index: number,
  registry: RegistryInfo
): NodeTypeSchema {
  const definition = registry.node_types.find((item) => item.type === nodeType);
  if (!definition) {
    const label = nodeID || `#${index + 1}`;
    throw new Error(`Cannot migrate node ${JSON.stringify(label)}: current registry does not provide type ${JSON.stringify(nodeType)}.`);
  }
  return definition;
}

function migrateGraphConditionV1(
  source: Record<string, unknown>,
  edgeIndex: number,
  registry: RegistryInfo,
  warnings: string[]
) {
  const sourceType = stringValue(source.type);
  const config = isPlainRecord(source.config) ? cloneJSONValue(source.config) : {};
  const scope = legacyScope(config);
  let targetType = sourceType;
  let state: Record<string, StateBinding> = {};

  switch (sourceType) {
    case "last_message_has_tool_calls":
      targetType = "conversation_has_tool_calls";
      state = { conversation: { path: legacyConversationPath(scope) } };
      delete config.state_scope;
      break;
    case "has_final_answer":
      targetType = "conversation_has_final_answer";
      state = { conversation: { path: legacyConversationPath(scope) } };
      delete config.state_scope;
      break;
    case "expression_conditions":
      if (!scope) {
        throw new Error(`Cannot migrate edge ${edgeIndex + 1}: expression_conditions with an empty state_scope cannot bind the shared state root in Graph Definition 2.0.`);
      }
      state = { state: { path: legacyScopePath(scope) } };
      migrateLegacyExpressions(config.expressions);
      delete config.state_scope;
      break;
    default:
      throw new Error(`Cannot migrate edge ${edgeIndex + 1}: unsupported legacy condition type ${JSON.stringify(sourceType)}.`);
  }

  const condition = requireRegisteredCondition(targetType, edgeIndex, registry);
  state = withDefaultStateBindings(state, condition, `edge_${edgeIndex + 1}`);
  if (targetType !== sourceType) {
    warnings.push(`Edge ${edgeIndex + 1} condition migrated from ${sourceType} to ${targetType}.`);
  }
  return {
    ...(stringValue(source.id) ? { id: stringValue(source.id) } : {}),
    type: targetType,
    ...(Object.keys(config).length > 0 ? { config } : {}),
    ...(Object.keys(state).length > 0 ? { state } : {}),
  };
}

function requireRegisteredCondition(
  conditionType: string,
  edgeIndex: number,
  registry: RegistryInfo
): ConditionSchema {
  const definition = registry.conditions.find((item) => item.type === conditionType);
  if (!definition) {
    throw new Error(`Cannot migrate edge ${edgeIndex + 1}: current registry does not provide condition ${JSON.stringify(conditionType)}.`);
  }
  return definition;
}

function withDefaultStateBindings(
  explicit: Record<string, StateBinding>,
  definition: NodeTypeSchema | ConditionSchema,
  ownerID: string
): Record<string, StateBinding> {
  const state = { ...explicit };
  for (const port of definition.state_ports ?? []) {
    if (state[port.name]?.path.trim()) continue;
    if (!port.required) continue;
    const defaultPath = stringValue(port.default_path).replaceAll("{node_id}", ownerID.replaceAll(".", "_"));
    if (defaultPath) {
      state[port.name] = { path: defaultPath };
      continue;
    }
    throw new Error(`Cannot migrate ${JSON.stringify(ownerID)}: required state binding ${JSON.stringify(port.name)} has no legacy mapping.`);
  }
  return state;
}

function migrateLegacyExpressions(value: unknown) {
  if (!Array.isArray(value)) {
    throw new Error("Cannot migrate expression_conditions: expressions must be an array.");
  }
  for (const item of value) {
    if (!isPlainRecord(item)) {
      throw new Error("Cannot migrate expression_conditions: expression items must be objects.");
    }
    if (Array.isArray(item.children)) migrateLegacyExpressions(item.children);
    const path = stringValue(item.value1);
    if (!path) continue;
    if (isAbsoluteStatePath(path)) {
      throw new Error(`Cannot migrate expression_conditions absolute path ${JSON.stringify(path)} automatically.`);
    }
    if (isLegacyConversationField(path)) item.value1 = `conversation.${path}`;
  }
}

function legacyScope(config: Record<string, unknown>): string {
  return Object.prototype.hasOwnProperty.call(config, "state_scope")
    ? stringValue(config.state_scope)
    : legacyDefaultScope;
}

function legacyConversationPath(scope: string): string {
  return scope ? `scopes.${scope}.conversation` : "shared.conversation";
}

function legacyScopePath(scope: string): string {
  return `scopes.${scope}`;
}

function isAbsoluteStatePath(path: string): boolean {
  return ["shared", "scopes", "runtime", "internal"].some((section) => path === section || path.startsWith(`${section}.`));
}

function isLegacyConversationField(path: string): boolean {
  const first = path.split(".", 1)[0];
  return first === "messages" || first === "final_answer" || first === "iteration_count" || first === "max_iterations";
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
      api_format: stringValue(item.api_format) || "chat_completions",
      model: stringValue(item.model),
      base_url: stringValue(item.base_url),
      extra_body: isPlainRecord(item.extra_body) ? cloneJSONValue(item.extra_body) : undefined,
      pricing: parseModelPricing(item.pricing),
      api_key_configured: Boolean(apiKey || item.api_key_configured === true),
      api_key: apiKey || undefined,
    };
  });

  const environmentPresets = parseEnvironmentPresets(value.environment_presets);
  return {
    environment,
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
    return {
      ...(cloneJSONValue(item) as unknown as GraphExportTrigger),
      id,
      type,
      enabled: typeof item.enabled === "boolean" ? item.enabled : true,
      concurrency: concurrency ?? "parallel",
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

function remapTriggerCanvasPositions(
  definition: GraphDefinition,
  triggerIDMap: Map<string, string>
): GraphDefinition {
  if (triggerIDMap.size === 0 || !isPlainRecord(definition.metadata?.web)) return definition;
  const web = cloneJSONValue(definition.metadata.web);
  if (!isPlainRecord(web.trigger_nodes)) return definition;
  const triggerNodes: Record<string, unknown> = {};
  for (const [triggerID, position] of Object.entries(web.trigger_nodes)) {
    const nextTriggerID = triggerIDMap.get(triggerID);
    if (nextTriggerID) triggerNodes[nextTriggerID] = position;
  }
  if (Object.keys(triggerNodes).length > 0) web.trigger_nodes = triggerNodes;
  else delete web.trigger_nodes;
  return {
    ...definition,
    metadata: {
      ...definition.metadata,
      web,
    },
  };
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
