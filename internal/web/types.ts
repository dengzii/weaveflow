export interface ApiResponse<T> {
  data: T;
  error?: ApiErrorPayload;
}

export interface ApiErrorPayload {
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
  policy?: GraphExecutionPolicy;
  metadata?: Record<string, unknown>;
}

export interface GraphNodeSpec {
  id: string;
  name?: string;
  type?: string;
  description?: string;
  config?: Record<string, unknown>;
  state?: Record<string, StateBinding>;
  policy?: NodeExecutionPolicy;
}

export interface GraphEdgeSpec {
  from: string;
  to: string;
  condition?: GraphConditionSpec;
  failure?: FailureRouteSpec;
}

export interface FailureRouteSpec {
  stages?: Array<"node" | "condition">;
  error_classes?: string[];
  catch_all?: boolean;
}

export interface GraphConditionSpec {
  id?: string;
  type: string;
  config?: Record<string, unknown>;
  state?: Record<string, StateBinding>;
}

export interface RetryPolicy {
  max_attempts?: number;
  initial_interval?: string;
  max_interval?: string;
  backoff_multiplier?: number;
  jitter?: number;
  retryable_error_classes?: string[];
  non_retryable_error_classes?: string[];
}

export interface NodeExecutionPolicy {
  timeout?: string;
  max_concurrency?: number;
  retry?: RetryPolicy;
}

export interface GraphLimits {
  max_super_steps?: number;
  max_node_executions?: number;
  max_fan_out?: number;
  max_concurrent_runs?: number;
  max_concurrent_nodes?: number;
  max_concurrent_tools?: number;
  max_state_bytes?: number;
  max_wall_time?: string;
}

export interface GraphExecutionPolicy {
  limits?: GraphLimits;
  node_defaults?: NodeExecutionPolicy;
}

export interface StateModuleRef {
  name: string;
  version: string;
}

export interface StateBinding {
  path: string;
  reducer?: string;
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
  tool_permissions: string[];
  tool_approvals: Record<string, boolean>;
}

export interface RuntimeModelPricing {
  currency?: string;
  input_per_million?: number;
  cached_input_per_million?: number;
  output_per_million?: number;
}

export type TriggerType = "webhook" | "schedule" | "chat";
export type TriggerConcurrency = "parallel" | "skip";
export type RunStatus = "pending" | "running" | "paused" | "failed" | "completed" | "canceled";
export type StepStatus = "scheduled" | "running" | "succeeded" | "failed" | "paused" | "canceled";

export interface TriggerTarget {
  graph_id: string;
}

export interface WebhookStateMapping {
  parameter: string;
  state_path: string;
}

export interface TriggerWebhookSpec {
  api_key?: string;
  state_bindings?: TriggerRequestStateBindings;
  state_mappings?: WebhookStateMapping[];
}

export interface TriggerRequestStateBindings {
  input?: string;
  metadata?: string;
  trigger_id?: string;
  trigger_type?: string;
  raw_body?: string;
}

export interface TriggerScheduleSpec {
  cron: string;
  timezone?: string;
  input?: Record<string, unknown>;
  state_bindings?: TriggerRequestStateBindings;
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
  input?: string;
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

export interface RuntimeEnvironmentPreset {
  key: string;
  default_value: string;
  type: "string" | "boolean" | "integer";
}

export interface RuntimeModelSettings {
  id: string;
  enabled: boolean;
  provider: string;
  api_format?: string;
  model?: string;
  base_url?: string;
  extra_body?: Record<string, unknown>;
  pricing?: RuntimeModelPricing;
  api_key_configured: boolean;
  api_key?: string;
}

export interface RuntimeSettingsUpdate {
  environment?: Record<string, string>;
  models?: RuntimeModelSettingsUpdate[];
  tool_permissions?: string[];
  tool_approvals?: Record<string, boolean>;
}

export interface RuntimeModelSettingsUpdate {
  id?: string;
  enabled?: boolean;
  provider?: string;
  api_format?: string;
  model?: string;
  base_url?: string;
  extra_body?: Record<string, unknown>;
  pricing?: RuntimeModelPricing;
  api_key?: string;
}

export interface GraphLoadResult {
  graph: GraphInfo;
  definition: GraphDefinition;
  settings: RuntimeSettings;
  runner_base_dir?: string;
  warnings?: WarningRecord[];
}

export interface GraphSessionSummary {
  id: string;
  created_at: string;
}

export interface GraphDetail {
  graph: GraphInfo;
  definition: GraphDefinition;
  settings: RuntimeSettings;
  initial_state_requirements: InitialStateRequirements;
  latest_session: GraphSessionSummary;
  active: {
    active_run_count: number;
    session_ids?: string[];
  };
}

export interface InitialStateRequirements {
  required: InitialStateRequirement[];
  provided_by_entry: InitialStateRequirement[];
  provided_by_upstream: InitialStateRequirement[];
  unresolved: InitialStateRequirement[];
  warnings?: WarningRecord[];
}

export interface TriggerInitialStateRequirements {
  trigger_id: string;
  requirements: InitialStateRequirements;
}

export interface GraphInitialStateAnalysis {
  direct: InitialStateRequirements;
  triggers: TriggerInitialStateRequirements[];
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
  reducers?: string[];
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
  output_schema?: unknown;
  strict?: boolean;
  permissions?: string[];
  approval?: "" | "never" | "required";
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
  reducer?: string;
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
  reducer?: string;
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
  required?: boolean;
  schema: Record<string, unknown>;
  mode: StateAccessMode;
  merge_strategy: StateMergeStrategy;
  reducer?: string;
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
  revision: number;
  parent_run_id?: string;
  parent_step_id?: string;
  parent_task_id?: string;
  root_run_id: string;
  run_path: string[];
  namespace: string;
  child_run_ids?: string[];
  return_value?: unknown;
  graph_id: string;
  graph_version: string;
  graph_hash?: string;
  graph_snapshot_hash?: string;
  graph_session_id?: string;
  origin?: RunOrigin;
  status: RunStatus;
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

export interface RunOrigin {
  type: string;
  trigger_id?: string;
}

export interface StepRecord {
  step_id: string;
  run_id: string;
  task_id: string;
  parent_run_id?: string;
  parent_step_id?: string;
  parent_task_id?: string;
  root_run_id?: string;
  run_path?: string[];
  namespace?: string;
  node_id: string;
  node_name: string;
  wave_id?: string;
  status: StepStatus;
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
  task_id?: string;
  parent_run_id?: string;
  parent_step_id?: string;
  parent_task_id?: string;
  root_run_id?: string;
  run_path?: string[];
  namespace?: string;
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
  parent_run_id?: string;
  parent_step_id?: string;
  parent_task_id?: string;
  root_run_id?: string;
  run_path?: string[];
  namespace?: string;
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
  graph_id?: string;
  graph_session_id?: string;
  run_id: string;
  parent_run_id?: string;
  parent_step_id?: string;
  parent_task_id?: string;
  root_run_id?: string;
  run_path?: string[];
  namespace?: string;
  step_id?: string;
  task_id?: string;
  node_id?: string;
  type: string;
  timestamp: string;
  payload?: unknown;
}

export interface RuntimeEventPage {
  items: RuntimeEvent[];
  next_cursor: string;
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
  events: RuntimeEventPage;
  interrupt?: RunInterrupt;
}

export interface RunListPage {
  items: RunRecord[];
  next_cursor: string;
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
  name?: string;
  graph_version: string;
  node_count: number;
  session_count: number;
  latest_session: string;
  active_run_count: number;
  updated_at: string;
}

export interface GraphListPage {
  items: CachedGraphSummary[];
  next_cursor: string;
}
