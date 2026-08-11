import type {
  CachedGraphSummary,
  CheckpointRecord,
  GraphDefinition,
  GraphDetail,
  GraphInfo,
  GraphListPage,
  GraphLoadResult,
  RegistryInfo,
  RunInspection,
  RunInterrupt,
  RunListPage,
  RunRecord,
  RunResult,
  RuntimeEvent,
  RuntimeEventPage,
  RuntimeSettings,
  StepRecord,
  ToolsInfo,
} from "./types";

const RUN_STATUSES = new Set(["pending", "running", "paused", "failed", "completed", "canceled"]);
const STEP_STATUSES = new Set(["scheduled", "running", "succeeded", "failed", "paused", "canceled"]);

export function validateGraphListPage(value: unknown, source: string): GraphListPage {
  const page = requireRecord(value, source);
  requireArray(page.items, `${source}.items`).forEach((item, index) => {
    validateCachedGraphSummary(item, `${source}.items[${index}]`);
  });
  requireString(page.next_cursor, `${source}.next_cursor`);
  return value as GraphListPage;
}

export function validateGraphDetail(value: unknown, source: string): GraphDetail {
  const detail = requireRecord(value, source);
  validateGraphInfo(detail.graph, `${source}.graph`);
  validateGraphDefinition(detail.definition, `${source}.definition`);
  validateRuntimeSettings(detail.settings, `${source}.settings`);
  validateInitialStateRequirements(detail.initial_state_requirements, `${source}.initial_state_requirements`);
  const latestSession = requireRecord(detail.latest_session, `${source}.latest_session`);
  requireString(latestSession.id, `${source}.latest_session.id`);
  requireString(latestSession.created_at, `${source}.latest_session.created_at`);
  const active = requireRecord(detail.active, `${source}.active`);
  requireNumber(active.active_run_count, `${source}.active.active_run_count`);
  requireOptionalStringArray(active.session_ids, `${source}.active.session_ids`);
  return value as GraphDetail;
}

export function validateGraphLoadResult(value: unknown, source: string): GraphLoadResult {
  const result = requireRecord(value, source);
  validateGraphInfo(result.graph, `${source}.graph`);
  validateGraphDefinition(result.definition, `${source}.definition`);
  validateRuntimeSettings(result.settings, `${source}.settings`);
  requireOptionalString(result.runner_base_dir, `${source}.runner_base_dir`);
  requireOptionalArray(result.warnings, `${source}.warnings`);
  return value as GraphLoadResult;
}

export function validateRunListPage(value: unknown, source: string): RunListPage {
  const page = requireRecord(value, source);
  requireArray(page.items, `${source}.items`).forEach((item, index) => {
    validateRunRecord(item, `${source}.items[${index}]`);
  });
  requireString(page.next_cursor, `${source}.next_cursor`);
  return value as RunListPage;
}

export function validateRunRecord(value: unknown, source: string): RunRecord {
  const run = requireRecord(value, source);
  requireString(run.run_id, `${source}.run_id`, false);
  requireString(run.graph_id, `${source}.graph_id`, false);
  requireString(run.graph_version, `${source}.graph_version`, false);
  const status = requireString(run.status, `${source}.status`, false);
  if (!RUN_STATUSES.has(status)) invalid(`${source}.status`, "a known run status");
  requireString(run.entry_node_id, `${source}.entry_node_id`, false);
  requireString(run.started_at, `${source}.started_at`, false);
  requireString(run.updated_at, `${source}.updated_at`, false);
  requireOptionalString(run.finished_at, `${source}.finished_at`);
  requireOptionalStringArray(run.current_node_ids, `${source}.current_node_ids`);
  requireOptionalStringArray(run.current_step_ids, `${source}.current_step_ids`);
  requireOptionalStringArray(run.next_node_ids, `${source}.next_node_ids`);
  return value as RunRecord;
}

export function validateRunResult(value: unknown, source: string): RunResult {
  const result = requireRecord(value, source);
  validateRunRecord(result.run, `${source}.run`);
  if (result.interrupt !== undefined) validateRunInterrupt(result.interrupt, `${source}.interrupt`);
  return value as RunResult;
}

export function validateRunInspection(value: unknown, source: string): RunInspection {
  const inspection = requireRecord(value, source);
  validateRunRecord(inspection.run, `${source}.run`);
  requireArray(inspection.steps, `${source}.steps`).forEach((step, index) => {
    validateStepRecord(step, `${source}.steps[${index}]`);
  });
  requireArray(inspection.checkpoints, `${source}.checkpoints`).forEach((checkpoint, index) => {
    validateCheckpointRecord(checkpoint, `${source}.checkpoints[${index}]`);
  });
  validateRuntimeEventPage(inspection.events, `${source}.events`);
  if (inspection.interrupt !== undefined) validateRunInterrupt(inspection.interrupt, `${source}.interrupt`);
  return value as RunInspection;
}

export function validateRuntimeEventPage(value: unknown, source: string): RuntimeEventPage {
  const page = requireRecord(value, source);
  requireArray(page.items, `${source}.items`).forEach((event, index) => {
    validateRuntimeEvent(event, `${source}.items[${index}]`);
  });
  requireString(page.next_cursor, `${source}.next_cursor`);
  return value as RuntimeEventPage;
}

export function validateRegistryInfo(value: unknown, source: string): RegistryInfo {
  const registry = requireRecord(value, source);
  requireArray(registry.state_modules, `${source}.state_modules`);
  requireArray(registry.capabilities, `${source}.capabilities`);
  requireArray(registry.node_groups, `${source}.node_groups`);
  requireArray(registry.node_types, `${source}.node_types`).forEach((nodeType, index) => {
    const record = requireRecord(nodeType, `${source}.node_types[${index}]`);
    requireString(record.type, `${source}.node_types[${index}].type`, false);
  });
  requireArray(registry.conditions, `${source}.conditions`).forEach((condition, index) => {
    const record = requireRecord(condition, `${source}.conditions[${index}]`);
    requireString(record.type, `${source}.conditions[${index}].type`, false);
  });
  requireRecord(registry.graph_schema, `${source}.graph_schema`);
  requireOptionalArray(registry.chat_channels, `${source}.chat_channels`);
  return value as RegistryInfo;
}

export function validateToolsInfo(value: unknown, source: string): ToolsInfo {
  const tools = requireRecord(value, source);
  requireArray(tools.tools, `${source}.tools`).forEach((tool, index) => {
    const record = requireRecord(tool, `${source}.tools[${index}]`);
    requireString(record.id, `${source}.tools[${index}].id`, false);
  });
  return value as ToolsInfo;
}

function validateCachedGraphSummary(value: unknown, source: string): CachedGraphSummary {
  const graph = requireRecord(value, source);
  requireString(graph.id, `${source}.id`, false);
  requireString(graph.graph_version, `${source}.graph_version`, false);
  requireNumber(graph.node_count, `${source}.node_count`);
  requireNumber(graph.session_count, `${source}.session_count`);
  requireString(graph.latest_session, `${source}.latest_session`, false);
  requireNumber(graph.active_run_count, `${source}.active_run_count`);
  requireString(graph.updated_at, `${source}.updated_at`, false);
  return value as CachedGraphSummary;
}

function validateGraphInfo(value: unknown, source: string): GraphInfo {
  const graph = requireRecord(value, source);
  requireString(graph.id, `${source}.id`, false);
  requireString(graph.version, `${source}.version`, false);
  return value as GraphInfo;
}

function validateGraphDefinition(value: unknown, source: string): GraphDefinition {
  const definition = requireRecord(value, source);
  requireArray(definition.nodes, `${source}.nodes`).forEach((node, index) => {
    const record = requireRecord(node, `${source}.nodes[${index}]`);
    requireString(record.id, `${source}.nodes[${index}].id`, false);
  });
  requireOptionalArray(definition.edges, `${source}.edges`);
  requireOptionalArray(definition.state_modules, `${source}.state_modules`);
  return value as GraphDefinition;
}

function validateRuntimeSettings(value: unknown, source: string): RuntimeSettings {
  const settings = requireRecord(value, source);
  requireRecord(settings.environment, `${source}.environment`);
  requireArray(settings.models, `${source}.models`);
  requireRecord(settings.memory, `${source}.memory`);
  return value as RuntimeSettings;
}

function validateInitialStateRequirements(value: unknown, source: string): void {
  const requirements = requireRecord(value, source);
  requireArray(requirements.required, `${source}.required`);
  requireArray(requirements.provided_by_entry, `${source}.provided_by_entry`);
  requireArray(requirements.provided_by_upstream, `${source}.provided_by_upstream`);
  requireArray(requirements.unresolved, `${source}.unresolved`);
}

function validateStepRecord(value: unknown, source: string): StepRecord {
  const step = requireRecord(value, source);
  requireString(step.step_id, `${source}.step_id`, false);
  requireString(step.run_id, `${source}.run_id`, false);
  requireString(step.node_id, `${source}.node_id`, false);
  requireString(step.node_name, `${source}.node_name`);
  const status = requireString(step.status, `${source}.status`, false);
  if (!STEP_STATUSES.has(status)) invalid(`${source}.status`, "a known step status");
  requireNumber(step.attempt, `${source}.attempt`);
  requireString(step.started_at, `${source}.started_at`, false);
  requireString(step.updated_at, `${source}.updated_at`, false);
  return value as StepRecord;
}

function validateCheckpointRecord(value: unknown, source: string): CheckpointRecord {
  const checkpoint = requireRecord(value, source);
  requireString(checkpoint.checkpoint_id, `${source}.checkpoint_id`, false);
  requireString(checkpoint.run_id, `${source}.run_id`, false);
  requireString(checkpoint.step_id, `${source}.step_id`);
  requireString(checkpoint.node_id, `${source}.node_id`);
  requireString(checkpoint.stage, `${source}.stage`, false);
  requireString(checkpoint.state_codec, `${source}.state_codec`);
  requireString(checkpoint.state_version, `${source}.state_version`);
  requireString(checkpoint.created_at, `${source}.created_at`, false);
  return value as CheckpointRecord;
}

function validateRuntimeEvent(value: unknown, source: string): RuntimeEvent {
  const event = requireRecord(value, source);
  requireString(event.id, `${source}.id`, false);
  requireString(event.run_id, `${source}.run_id`, false);
  requireString(event.type, `${source}.type`, false);
  requireString(event.timestamp, `${source}.timestamp`, false);
  return value as RuntimeEvent;
}

function validateRunInterrupt(value: unknown, source: string): RunInterrupt {
  const interrupt = requireRecord(value, source);
  requireString(interrupt.run_id, `${source}.run_id`, false);
  requireString(interrupt.checkpoint_id, `${source}.checkpoint_id`, false);
  return value as RunInterrupt;
}

function requireRecord(value: unknown, source: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) invalid(source, "an object");
  return value as Record<string, unknown>;
}

function requireArray(value: unknown, source: string): unknown[] {
  if (!Array.isArray(value)) invalid(source, "an array");
  return value;
}

function requireOptionalArray(value: unknown, source: string): void {
  if (value !== undefined) requireArray(value, source);
}

function requireString(value: unknown, source: string, allowEmpty = true): string {
  if (typeof value !== "string" || (!allowEmpty && !value.trim())) invalid(source, "a non-empty string");
  return value as string;
}

function requireOptionalString(value: unknown, source: string): void {
  if (value !== undefined) requireString(value, source);
}

function requireOptionalStringArray(value: unknown, source: string): void {
  if (value === undefined) return;
  requireArray(value, source).forEach((item, index) => requireString(item, `${source}[${index}]`, false));
}

function requireNumber(value: unknown, source: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) invalid(source, "a finite number");
  return value as number;
}

function invalid(source: string, expected: string): never {
  throw new Error(`invalid API response at ${source}: expected ${expected}`);
}
