export interface ApiResponse<T> {
  data?: T;
  error?: string;
}

export interface GraphDefinition {
  version?: string;
  name?: string;
  description?: string;
  state_modules?: StateModuleRef[];
  entry_point?: string;
  finish_point?: string;
  nodes: GraphNodeSpec[];
  edges?: GraphEdgeSpec[];
  metadata?: Record<string, unknown>;
}

export interface GraphNodeSpec {
  id: string;
  name?: string;
  type?: string;
  description?: string;
  config?: Record<string, unknown>;
  state?: Record<string, StateBinding>;
}

export interface GraphEdgeSpec {
  from: string;
  to: string;
  condition?: GraphConditionSpec;
}

export interface GraphConditionSpec {
  type: string;
  config?: Record<string, unknown>;
  state?: Record<string, StateBinding>;
}

export interface StateModuleRef {
  name: string;
  version: string;
}

export interface StateBinding {
  path: string;
}

export interface GraphInfo {
  id: string;
  version: string;
  graph_hash?: string;
  graph_snapshot_hash?: string;
  graph_session_id?: string;
  entry_point?: string;
  finish_point?: string;
}

export interface GraphSettings {
  environment: Record<string, string>;
  environment_presets?: GraphEnvironmentPreset[];
  model: GraphModelSettings;
  models: GraphModelSettings[];
  memory: GraphMemorySettings;
}

export interface GraphEnvironmentPreset {
  key: string;
  default_value: string;
  type: "string" | "boolean" | "integer";
}

export interface GraphModelSettings {
  id: string;
  enabled: boolean;
  provider: string;
  model?: string;
  base_url?: string;
  api_key_configured: boolean;
}

export interface GraphMemorySettings {
  enabled: boolean;
  directory?: string;
}

export interface GraphSettingsUpdate {
  environment?: Record<string, string>;
  models?: GraphModelSettingsUpdate[];
  memory?: {
    enabled?: boolean;
    directory?: string;
  };
}

export interface GraphModelSettingsUpdate {
  id?: string;
  enabled?: boolean;
  provider?: string;
  model?: string;
  base_url?: string;
  api_key?: string;
}

export interface GraphLoadResult {
  graph: GraphInfo;
  definition: GraphDefinition;
  runner_base_dir?: string;
  warnings?: WarningRecord[];
}

export interface InitialStateRequirements {
  required: InitialStateRequirement[];
  provided_by_upstream: InitialStateRequirement[];
  unresolved: InitialStateRequirement[];
  warnings?: WarningRecord[];
}

export interface InitialStateRequirement {
  path: string;
  nodes?: string[];
  sources?: string[];
  type?: string;
  description?: string;
  message?: string;
}

export interface RegistryInfo {
  state_modules: StateModuleDefinition[];
  capabilities: StateCapabilityDefinition[];
  node_types: NodeTypeSchema[];
  conditions: ConditionSchema[];
  graph_schema: Record<string, unknown>;
}

export interface ToolsInfo {
  tools: ToolDefinition[];
}

export interface ToolDefinition {
  id: string;
  name?: string;
  description?: string;
  parameters?: unknown;
  strict?: boolean;
}

export interface StateFieldDefinition {
  path: string;
  description?: string;
  schema: Record<string, unknown>;
}

export interface StateCapabilityFieldDefinition {
  name: string;
  schema: Record<string, unknown>;
  merge_strategy?: StateMergeStrategy;
}

export interface StateCapabilityDefinition {
  id: string;
  schema: Record<string, unknown>;
  fields: StateCapabilityFieldDefinition[];
}

export interface StateModuleDefinition {
  name: string;
  version: string;
  fields?: StateFieldDefinition[];
  capabilities?: StateCapabilityDefinition[];
}

export type StateAccessMode = "read" | "write" | "read_write";
export type StateMergeStrategy = "replace" | "merge" | "append";

export interface RelativeStateFieldRef {
  path: string;
  mode: StateAccessMode;
  required?: boolean;
}

export interface RelativeStateContract {
  fields?: RelativeStateFieldRef[];
}

export interface StatePortDefinition {
  name: string;
  description?: string;
  default_path?: string;
  required?: boolean;
  schema?: Record<string, unknown>;
  mode?: StateAccessMode;
  capability?: string;
  contract?: RelativeStateContract;
  merge_strategy?: StateMergeStrategy;
}

export interface NodeTypeSchema {
  type: string;
  title?: string;
  description?: string;
  config_schema?: Record<string, unknown>;
  state_ports?: StatePortDefinition[];
}

export interface ConditionSchema {
  type: string;
  title?: string;
  description?: string;
  config_schema?: Record<string, unknown>;
  state_ports?: StatePortDefinition[];
}

export interface RunRecord {
  run_id: string;
  graph_id: string;
  graph_version: string;
  graph_hash?: string;
  graph_snapshot_hash?: string;
  graph_session_id?: string;
  status: string;
  entry_node_id: string;
  current_node_id?: string;
  current_node_ids?: string[];
  current_step_ids?: string[];
  next_node_ids?: string[];
  parallel_wave_id?: string;
  last_step_id?: string;
  last_checkpoint_id?: string;
  pause_requested?: boolean;
  cancel_requested?: boolean;
  error_code?: string;
  error_message?: string;
  started_at: string;
  updated_at: string;
  finished_at?: string;
}

export interface StepRecord {
  step_id: string;
  run_id: string;
  node_id: string;
  node_name: string;
  status: string;
  attempt: number;
  checkpoint_before_id?: string;
  checkpoint_after_id?: string;
  error_code?: string;
  error_message?: string;
  started_at: string;
  updated_at: string;
  finished_at?: string;
}

export interface CheckpointRecord {
  checkpoint_id: string;
  run_id: string;
  step_id: string;
  node_id: string;
  stage: string;
  state_codec: string;
  state_version: string;
  payload_ref?: string;
  created_at: string;
}

export interface ArtifactRef {
  id: string;
  run_id?: string;
  step_id?: string;
  node_id?: string;
  type?: string;
  mime_type?: string;
  location?: string;
  created_at?: string;
}

export interface ArtifactDetail {
  artifact: ArtifactRef;
  size: number;
  text?: string;
  data_base64?: string;
}

export interface RuntimeEvent {
  id: string;
  run_id: string;
  step_id?: string;
  node_id?: string;
  type: string;
  timestamp: string;
  payload?: unknown;
}

export interface RunInterrupt {
  run_id: string;
  checkpoint_id: string;
  step_id?: string;
  node_id?: string;
  stage?: string;
  message?: string;
  resume_from_run_id?: string;
  resume_from_checkpoint_id?: string;
  breakpoint_hit?: unknown;
  runtime?: unknown;
}

export interface RunResult {
  run: RunRecord;
  state?: unknown;
  interrupt?: RunInterrupt;
}

export interface RunDetail {
  run: RunRecord;
  steps: StepRecord[];
  checkpoints: CheckpointRecord[];
  events: RuntimeEvent[];
  artifacts: ArtifactRef[];
  interrupt?: RunInterrupt;
}

export interface CheckpointDetail {
  record: CheckpointRecord;
  snapshot?: unknown;
  business?: unknown;
  runtime?: unknown;
  artifacts?: ArtifactRef[];
}

export interface WarningRecord {
  code?: string;
  message?: string;
  node_id?: string;
  other_node_id?: string;
  path?: string;
  sources?: string[];
}

export interface CachedGraphSummary {
  id: string;
  session_count: number;
  latest_session?: string;
}

