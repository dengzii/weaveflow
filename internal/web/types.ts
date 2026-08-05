export interface ApiResponse<T> {
  data: T;
  error?: ApiError;
}

export interface ApiError {
  code: string;
  message: string;
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

export interface RuntimeSettings {
  environment: Record<string, string>;
  environment_presets?: RuntimeEnvironmentPreset[];
  models: RuntimeModelSettings[];
  memory: RuntimeMemorySettings;
}

export type TriggerType = "webhook" | "schedule" | "chat";
export type TriggerConcurrency = "parallel" | "skip";

export interface TriggerTarget {
  graph_id: string;
}

export interface WebhookStateMapping {
  parameter: string;
  state_path: string;
}

export interface TriggerWebhookSpec {
  api_key?: string;
  state_mappings?: WebhookStateMapping[];
}

export interface TriggerScheduleSpec {
  cron: string;
  timezone?: string;
  input?: Record<string, unknown>;
}

export interface TriggerChatSpec {
  channel?: string;
  channel_config?: Record<string, unknown>;
  stream_updates?: boolean;
  stream_node_ids?: string[];
  history_limit?: number;
  state_bindings?: TriggerChatStateBindings;
}

export interface TriggerChatStateBindings {
  conversation?: string;
  raw_history?: string;
  trigger_id?: string;
  channel?: string;
  user_id?: string;
  conversation_id?: string;
  message_id?: string;
}

export interface ChatChannelDefinition {
  id: string;
  title: string;
  description?: string;
  config_schema: Record<string, unknown>;
  setup?: ChatChannelSetupDefinition;
}

export interface ChatChannelSetupDefinition {
  kind: "qr_code";
}

export type ChatChannelSetupStatus =
  | "waiting"
  | "scanned"
  | "verification_required"
  | "confirmed"
  | "expired"
  | "failed";

export interface ChatChannelSetupAccount {
  id?: string;
  label?: string;
}

export interface ChatChannelSetupResult {
  session_id: string;
  channel_id: string;
  status: ChatChannelSetupStatus;
  qr_code_content?: string;
  expires_at: string;
  account?: ChatChannelSetupAccount;
  message?: string;
}

export interface Trigger {
  id: string;
  name?: string;
  type: TriggerType;
  enabled: boolean;
  target?: TriggerTarget;
  concurrency?: TriggerConcurrency;
  initial_state?: Record<string, unknown>;
  webhook?: TriggerWebhookSpec;
  schedule?: TriggerScheduleSpec;
  chat?: TriggerChatSpec;
  created_at: string;
  updated_at: string;
}

export interface TriggerCanvasPosition {
  x: number;
  y: number;
}

export interface TriggerCanvasNode {
  canvas_id: string;
  label: string;
  trigger: Trigger;
  position: TriggerCanvasPosition;
  valid: boolean;
}

export interface TriggerInvocation {
  id: string;
  trigger_id: string;
  trigger_type: TriggerType;
  target: TriggerTarget;
  status: string;
  run?: RunRecord;
  error_message?: string;
  triggered_at: string;
  updated_at: string;
}

export interface RuntimeEnvironmentPreset {
  key: string;
  default_value: string;
  type: "string" | "boolean" | "integer";
}

export interface RuntimeModelSettings {
  id: string;
  enabled: boolean;
  provider: string;
  model?: string;
  base_url?: string;
  api_key_configured: boolean;
  api_key?: string;
}

export interface RuntimeMemorySettings {
  enabled: boolean;
  directory?: string;
}

export interface RuntimeSettingsUpdate {
  environment?: Record<string, string>;
  models?: RuntimeModelSettingsUpdate[];
  memory?: {
    enabled?: boolean;
    directory?: string;
  };
}

export interface RuntimeModelSettingsUpdate {
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
  settings: RuntimeSettings;
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
  node_groups: NodeGroup[];
  chat_channels?: ChatChannelDefinition[];
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

export interface NodeGroup {
  name: string;
  node_types: string[];
}

export interface DynamicStatePortDefinition {
  description?: string;
  name_pattern: string;
  min_ports?: number;
  max_ports?: number;
  schema: Record<string, unknown>;
  mode: StateAccessMode;
  merge_strategy: StateMergeStrategy;
}

export interface NodeTypeSchema {
  type: string;
  title?: string;
  description?: string;
  config_schema?: Record<string, unknown>;
  state_ports?: StatePortDefinition[];
  dynamic_state_ports?: DynamicStatePortDefinition;
}

export interface ConditionSchema {
  type: string;
  title?: string;
  description?: string;
  config_schema?: Record<string, unknown>;
  state_ports?: StatePortDefinition[];
  dynamic_state_ports?: DynamicStatePortDefinition;
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

export interface RunInspection {
  run: RunRecord;
  steps: StepRecord[];
  checkpoints: CheckpointRecord[];
  events: RuntimeEvent[];
  interrupt: RunInterrupt | null;
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
  graph_version: string;
  definition: GraphDefinition;
  settings: RuntimeSettings;
  session_count: number;
  latest_session: string;
  updated_at: string;
}

