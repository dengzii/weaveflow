export interface RunRecord {
  run_id: string;
  graph_id: string;
  graph_version: string;
  status: string;
  entry_node_id: string;
  current_node_id: string;
  last_step_id: string;
  error_message: string;
  started_at: string;
  finished_at?: string;
  updated_at: string;
}

export interface RunSummary {
  source_id: string;
  source_name: string;
  cache_root: string;
  instance_id: string;
  graph_ref: string;
  graph_version: string;
  run: RunRecord;
  duration_ms: number;
  step_count: number;
  event_count: number;
  checkpoint_count: number;
  artifact_count: number;
}

export interface SourceMeta {
  id: string;
  name: string;
  root: string;
  instance_id: string;
  graph_ref: string;
  graph_version: string;
  instance: unknown;
  graph: unknown;
  warnings: string[];
}

export interface EventView {
  id: string;
  run_id: string;
  step_id: string;
  node_id: string;
  type: string;
  timestamp: string;
  payload: unknown;
}

export interface ReplayItem {
  index: number;
  timestamp: string;
  level: string;
  title: string;
  subtitle: string;
  event: EventView;
}

export interface LiveState {
  running: boolean;
  run_id: string;
  source_name: string;
  graph_ref: string;
  started_at?: string;
  graph?: unknown;
  items: ReplayItem[];
}

export interface StepRecord {
  step_id: string;
  run_id: string;
  node_id: string;
  node_name: string;
  status: string;
  attempt: number;
  started_at: string;
  finished_at?: string;
  updated_at: string;
  checkpoint_before_id: string;
  checkpoint_after_id: string;
}

export interface StepView {
  record: StepRecord;
  duration_ms: number;
}

export interface CheckpointRecord {
  checkpoint_id: string;
  run_id: string;
  node_id: string;
  stage: string;
  created_at: string;
}

export interface CheckpointSummary {
  record: CheckpointRecord;
}

export interface ArtifactRef {
  id: string;
  run_id: string;
  step_id: string;
  node_id: string;
  type: string;
  mime_type: string;
  location: string;
  created_at: string;
}

export interface ArtifactSummary {
  ref: ArtifactRef;
  bytes: number;
}

export interface RunDetail {
  summary: RunSummary;
  source: SourceMeta;
  run: RunRecord;
  metadata?: unknown;
  steps: StepView[];
  events: EventView[];
  replay: ReplayItem[];
  checkpoints: CheckpointSummary[];
  artifacts: ArtifactSummary[];
}

export interface RunsResponse {
  cache_dir: string;
  sources: SourceMeta[];
  runs: RunSummary[];
}

export interface CacheFileEntry {
  path: string;
  name: string;
  size: number;
  modified_at: string;
  content_type: string;
  is_text: boolean;
  is_previewable: boolean;
}

export interface CacheFilesResponse {
  cache_dir: string;
  files: CacheFileEntry[];
}

export interface CacheFileDetail {
  cache_dir: string;
  path: string;
  name: string;
  size: number;
  modified_at: string;
  content_type: string;
  encoding: string;
  is_text: boolean;
  truncated: boolean;
  content: string;
}
