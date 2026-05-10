export interface Config {
  system_prompt: string;
  max_iterations: number;
  planner_max_steps: number;
  memory_recall_limit: number;
  tools: Record<string, boolean>;
  mode: "auto" | "direct" | "planner";
}

export interface ApiResponse<T> {
  code: number;
  data: T;
}

export interface MemoryEntry {
  id: string;
  text: string;
  role: string;
  payload?: Record<string, unknown>;
  created_at: number;
  type: string;
  tags?: string[];
}

export interface HistoryPart {
  type: string;
  text?: string;
  name?: string;
  result?: string;
  id?: string;
}

export interface HistoryMessage {
  role: string;
  parts: HistoryPart[];
  status?: string;
  created_at?: number;
}

export interface ChatEvent {
  type: string;
  content?: string;
  data?: Record<string, string>;
}

export interface RegistryFieldRef {
  path: string;
  mode: string;
  required?: boolean;
  description?: string;
  merge_strategy?: string;
  dynamic?: boolean;
  path_config_key?: string;
  schema?: Record<string, unknown>;
}

export interface RegistryStateContract {
  fields: RegistryFieldRef[];
}

export interface RegistryNodeTypeSchema {
  type: string;
  title?: string;
  description?: string;
  config_schema: Record<string, unknown>;
  state_contract?: RegistryStateContract;
}

export interface RegistryNodeTypeInfo {
  schema: RegistryNodeTypeSchema;
  example_config?: Record<string, unknown>;
  resolved_state_contract?: RegistryStateContract;
  resolve_error?: string;
}

export interface RegistryStateFieldInfo {
  name: string;
  description?: string;
  schema: Record<string, unknown>;
}

export interface RegistryConditionSchema {
  type: string;
  title?: string;
  description?: string;
  config_schema: Record<string, unknown>;
}

export interface RegistryData {
  state_fields: RegistryStateFieldInfo[];
  node_types: RegistryNodeTypeInfo[];
  conditions: RegistryConditionSchema[];
  graph_schema: Record<string, unknown>;
}

export type MessageItem =
  | { id: string; kind: "user"; text: string }
  | { id: string; kind: "step"; text: string; status: "pending" | "done" }
  | { id: string; kind: "thinking"; text: string; done: boolean }
  | {
      id: string;
      kind: "tool";
      toolCallId?: string;
      name: string;
      status: "calling" | "done" | "error";
      args: string;
      output: string;
      error: string;
    }
  | { id: string; kind: "assistant"; text: string }
  | { id: string; kind: "error"; text: string }
  | { id: string; kind: "stopped" };
