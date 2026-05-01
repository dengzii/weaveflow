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

export interface HistoryPart {
  type: string;
  text?: string;
  name?: string;
  result?: unknown;
}

export interface HistoryMessage {
  role: string;
  parts: HistoryPart[];
}

export interface ChatEvent {
  type: string;
  message?: string;
  content?: string;
  node_id?: string;
  timestamp?: string;
}

export type TimelineEntry =
  | { kind: "phase"; icon: string; text: string; cls: string }
  | { id: string; kind: "tool"; name: string; status: "pending" | "ok" | "err"; result: string };

export type MessageItem =
  | { id: string; kind: "user"; text: string }
  | { id: string; kind: "thinking"; text: string; done: boolean }
  | { id: string; kind: "timeline"; entries: TimelineEntry[] }
  | { id: string; kind: "assistant"; text: string }
  | { id: string; kind: "error"; text: string }
  | { id: string; kind: "stopped" };
