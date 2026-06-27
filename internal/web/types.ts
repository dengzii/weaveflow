export interface ApiResponse<T> {
  data?: T;
  error?: string;
}

export interface GraphDefinition {
  version?: string;
  name?: string;
  description?: string;
  state_schema?: string;
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
}

export interface GraphEdgeSpec {
  from: string;
  to: string;
  condition?: GraphConditionSpec;
}

export interface GraphConditionSpec {
  type: string;
  config?: Record<string, unknown>;
}

export interface GraphInfo {
  id: string;
  version: string;
  entry_point?: string;
  finish_point?: string;
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
  state_fields: StateFieldDefinition[];
  node_types: NodeTypeSchema[];
  conditions: ConditionSchema[];
  graph_schema: Record<string, unknown>;
}

export interface StateFieldDefinition {
  name: string;
  description?: string;
  schema: Record<string, unknown>;
}

export interface NodeTypeSchema {
  type: string;
  title?: string;
  description?: string;
  config_schema?: Record<string, unknown>;
  state_contract?: unknown;
}

export interface ConditionSchema {
  type: string;
  title?: string;
  description?: string;
  config_schema?: Record<string, unknown>;
}

export interface RunRecord {
  run_id: string;
  graph_id: string;
  graph_version: string;
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

export interface RunResult {
  run: RunRecord;
  state?: unknown;
}

export interface RunDetail {
  run: RunRecord;
  steps: StepRecord[];
  checkpoints: CheckpointRecord[];
  events: RuntimeEvent[];
  artifacts: ArtifactRef[];
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
